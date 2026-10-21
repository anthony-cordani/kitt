package dependency

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
	"golang.org/x/mod/semver"
)

// Release is a published plain X.Y.Z version and its exact commit.
type Release struct{ Version, Commit string }

// SourceAccess supplies published releases and metadata at an exact commit.
// Implementations must not read requirements from a branch tip instead of commit.
// Requires should return a ValidationError for invalid release frontmatter.
type SourceAccess interface {
	Releases(source, skill string) ([]Release, error)
	Requires(source, skill, commit string) ([]string, error)
}

// PinConstraintAccess optionally supplies fixed-pin constraints after the target
// list is known. Constraint errors are planning errors, never source failures.
// Fixed pins participate in search conflicts so other candidates can be tried.
type PinConstraintAccess interface {
	PinConstraints(targets []Target) (map[string]bool, error)
}

// TipAccess optionally supports a new, unconstrained direct install without tags.
// A branch tip can never satisfy a dependency's explicit range.
type TipAccess interface {
	Tip(source, skill string) (Release, error)
}

// CandidatePolicy controls explicit targets. Install without a constraint retains
// transitive pins on promotion and re-resolves direct pins. Upgrade selects the
// highest permitted release.
type CandidatePolicy string

const (
	Install CandidatePolicy = "install"
	Upgrade CandidatePolicy = "upgrade"
)

// Target requests a direct skill. Source may be omitted for an existing entry or
// a manifest with one source. Constraint replaces that entry's explicit constraint
// when nonempty. A no-range install re-resolves a direct entry; promotion keeps
// a transitive entry's constraint and pin.
// Callers can supply a caret constraint to limit an upgrade to a selected major.
type Target struct {
	Name, Source, Constraint string
	Policy                   CandidatePolicy
	// PreserveDirect keeps an existing transitive entry transitive. Installs
	// retain their default promotion behavior.
	PreserveDirect bool
	// BoundByDependents selects the highest candidate within incoming ranges,
	// rather than proposing a candidate independently of those ranges.
	BoundByDependents bool
	// AllowMajor removes only the target's own caret range, retaining incoming ranges.
	AllowMajor bool
}

// Node is the final pin and derived graph data. Requires is normalized, sorted,
// unique, and always nonnil. Via contains immediate parents, sorted and unique.
type Node struct {
	Name, Source, Constraint string
	Version, Commit          string
	Requires                 []string
	Direct                   bool
	Via                      []string
}

// Diff compares all planned fields with the manifest, including snapshots and via.
type Diff struct{ Added, Changed, Unchanged []Node }

// Plan is deterministic: every node list and Orphans is ordered by skill name.
// Existing orphaned transitive entries are retained and still constrain the graph.
type Plan struct {
	Nodes   []Node
	Diff    Diff
	Orphans []string
}

type planner struct {
	scope        Scope
	current      *manifest.Manifest
	access       SourceAccess
	seeds        map[string]*manifest.Skill
	targets      map[string]Target
	locked       map[string]Release
	releases     map[string][]Release
	requires     map[string][]string
	accessErrors map[string]error
	indexes      map[string]map[string]int
	explored     int
	movable      map[string]bool
	fixed        map[string]bool
}

// BuildPlan plans one scope without writing managed state or mutating inputs.
// All current entries participate, including orphans. Source access is lazy and
// memoized per invocation; known snapshots on retained pins require no access.
// Explicit targets use their policy's preferred candidate. A new install may
// try an older release to retain an existing compatible pin; other dependency
// failures never lower explicit targets. An error always returns a nil plan.
func BuildPlan(scope Scope, current *manifest.Manifest, targets []Target, access SourceAccess) (result *Plan, planErr error) {
	var p *planner
	defer func() {
		if planErr != nil {
			var f *searchFailure
			if errors.As(planErr, &f) {
				planErr = f.err
			}
			explored := 0
			if p != nil {
				explored = p.explored
			}
			var access *AccessError
			if !errors.As(planErr, &access) {
				planErr = &PlanError{Err: planErr, ManagedStateUntouched: true, ExploredStates: explored}
			}
		}
	}()
	if current == nil {
		return nil, &ValidationError{Location: "manifest", Err: fmt.Errorf("must not be nil")}
	}
	p = &planner{
		scope: scope, current: current, access: access,
		seeds: make(map[string]*manifest.Skill), targets: make(map[string]Target), locked: make(map[string]Release),
		releases: make(map[string][]Release), requires: make(map[string][]string), movable: make(map[string]bool),
		indexes:      make(map[string]map[string]int),
		accessErrors: make(map[string]error),
	}
	if err := p.prepare(targets); err != nil {
		return nil, err
	}
	if constraints, ok := access.(PinConstraintAccess); ok {
		var err error
		p.fixed, err = constraints.PinConstraints(targets)
		if err != nil {
			return nil, err
		}
	}
	if err := p.preparePins(); err != nil {
		return nil, err
	}
	selected, err := p.search(map[string]assignment{})
	if err != nil {
		return nil, err
	}
	nodes := make(map[string]Node, len(selected))
	for name, selection := range selected {
		nodes[name] = selection.Node
	}
	return p.plan(nodes), nil
}

func keys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func (p *planner) validateName(alias, name string) error {
	// Manifest and CLI aliases retain their legacy grammar.
	err := skill.ValidName(name)
	if err != nil {
		return &ValidationError{Location: "manifest skill " + name, Err: err}
	}
	if _, ok := p.current.Sources[alias]; !ok {
		return &UnknownSourceError{Scope: p.scope, Alias: alias}
	}
	return nil
}

func (p *planner) prepare(targets []Target) error {
	for _, name := range keys(p.current.Skills) {
		s := p.current.Skills[name]
		if s == nil {
			return &ValidationError{Location: "manifest skill " + name, Err: fmt.Errorf("must not be nil")}
		}
		if err := p.validateName(s.Source, name); err != nil {
			return err
		}
		if err := source.ValidateConstraint(s.Version); err != nil && s.Resolved == nil {
			return &ValidationError{Location: "manifest skill " + name + " version", Err: err}
		}
		if s.Resolved != nil {
			if s.Resolved.Commit == "" {
				return &ValidationError{Location: "manifest skill " + name, Err: fmt.Errorf("pin has no commit")}
			}
			if s.Resolved.Version != "" {
				if _, err := source.Satisfies("", s.Resolved.Version); err != nil {
					return &ValidationError{Location: "manifest skill " + name, Err: err}
				}
			}
			if s.Resolved.Requires != nil {
				n := Node{Name: name, Source: s.Source, Version: s.Resolved.Version, Commit: s.Resolved.Commit}
				if _, err := p.normalize(n, *s.Resolved.Requires, true); err != nil {
					return err
				}
			}
		}
		copy := *s
		p.seeds[name] = &copy
	}
	ordered := slices.Clone(targets)
	slices.SortFunc(ordered, func(a, b Target) int { return strings.Compare(a.Name, b.Name) })
	for _, t := range ordered {
		if _, ok := p.targets[t.Name]; ok {
			return &ValidationError{Location: "target " + t.Name, Err: fmt.Errorf("duplicate target")}
		}
		if t.Policy == "" {
			t.Policy = Install
		}
		if t.Policy != Install && t.Policy != Upgrade {
			return &ValidationError{Location: "target " + t.Name, Err: fmt.Errorf("unknown candidate policy %q", t.Policy)}
		}
		s, exists := p.seeds[t.Name]
		if t.Source == "" {
			if exists {
				t.Source = s.Source
			} else if len(p.current.Sources) == 1 {
				t.Source = keys(p.current.Sources)[0]
			}
		}
		if t.Source == "" {
			return &ValidationError{Location: "target " + t.Name, Err: fmt.Errorf("specify an alias/ qualifier; configured source aliases: %s", strings.Join(keys(p.current.Sources), ", "))}
		}
		if err := p.validateName(t.Source, t.Name); err != nil {
			return err
		}
		if err := source.ValidateConstraint(t.Constraint); err != nil {
			return &ValidationError{Location: "target " + t.Name, Err: err}
		}
		if exists && s.Source != t.Source {
			// The final graph still contains every dependent's source requirement.
			// A collision there blocks a switch; an unreferenced entry is replaceable.
			s.Source, s.Version, s.Resolved = t.Source, "", nil
		}
		if !exists {
			s = &manifest.Skill{Source: t.Source}
			p.seeds[t.Name] = s
		}
		if t.AllowMajor || (t.Policy == Install && t.Constraint == "" && s.IsDirect()) {
			s.Version = ""
		}
		if t.Constraint != "" {
			s.Version = t.Constraint
		}
		if err := source.ValidateConstraint(s.Version); err != nil {
			return &ValidationError{Location: "manifest skill " + t.Name + " version", Err: err}
		}
		if exists && s.IsDirect() && t.Policy == Install && t.Constraint == "" {
			t.BoundByDependents = true
		}
		p.targets[t.Name] = t
	}
	return nil
}

// normalize uses Unit 1 parsing/grouping, preserving original indexes in errors.
func (p *planner) normalize(n Node, raw []string, snapshot bool) ([]string, error) {
	out, err := NormalizeRequirements(raw, n.Source, snapshot)
	if err != nil {
		return nil, &ValidationError{Location: nodeRef(n), Err: err}
	}
	indexes := make(map[string]int)
	for i, text := range raw {
		r, _ := ParseSnapshotRef(text)
		r = r.WithDefaultSource(n.Source)
		if _, ok := p.current.Sources[r.Source]; !ok {
			return nil, &UnknownSourceError{Scope: p.scope, Alias: r.Source, Requirement: incoming(n, r, i)}
		}
		if _, ok := indexes[r.String()]; !ok {
			indexes[r.String()] = i
		}
	}
	p.indexes[nodeRef(n)] = indexes
	return out, nil
}

func nodeRef(n Node) string {
	v := n.Version
	if v == "" {
		v = n.Commit
	}
	return n.Source + "/" + n.Name + "@" + v
}

func incoming(n Node, ref Ref, index int) Requirement {
	return Requirement{Ref: ref, Requester: n.Name, Source: n.Source, Version: n.Version, Commit: n.Commit, Index: index}
}

func (p *planner) load(n Node) (Node, error) {
	var raw []string
	snapshot := false
	if s := p.current.Skills[n.Name]; s != nil && s.Source == n.Source && s.Resolved != nil && s.Resolved.Commit == n.Commit && s.Resolved.Requires != nil {
		raw = *s.Resolved.Requires
		snapshot = true
	} else {
		key := n.Source + "/" + n.Name + "@" + n.Commit
		if err := p.accessErrors["requires "+key]; err != nil {
			return Node{}, err
		}
		var ok bool
		raw, ok = p.requires[key]
		if !ok {
			if p.access == nil {
				return Node{}, &AccessError{Source: n.Source, Name: n.Name, Commit: n.Commit, Operation: "read requirements", Err: fmt.Errorf("no source access configured")}
			}
			var err error
			raw, err = p.access.Requires(n.Source, n.Name, n.Commit)
			if err != nil {
				var validation *ValidationError
				if errors.As(err, &validation) {
					failure := &candidateValidationError{&ValidationError{Location: nodeRef(n) + " " + n.Name + "/SKILL.md", Err: err}}
					p.accessErrors["requires "+key] = failure
					return Node{}, failure
				}
				failure := &AccessError{Source: n.Source, Name: n.Name, Commit: n.Commit, Operation: "read requirements", Err: err}
				p.accessErrors["requires "+key] = failure
				return Node{}, failure
			}
			p.requires[key] = slices.Clone(raw)
		}
	}
	var err error
	n.Requires, err = p.normalize(n, raw, snapshot)
	return n, err
}

func (p *planner) published(alias, name string) ([]Release, error) {
	key := alias + "/" + name
	if err := p.accessErrors["releases "+key]; err != nil {
		return nil, err
	}
	if cached, ok := p.releases[key]; ok {
		return cached, nil
	}
	if p.access == nil {
		return nil, &AccessError{Source: alias, Name: name, Operation: "list releases", Err: fmt.Errorf("no source access configured")}
	}
	releases, err := p.access.Releases(alias, name)
	if err != nil {
		failure := &AccessError{Source: alias, Name: name, Operation: "list releases", Err: err}
		p.accessErrors["releases "+key] = failure
		return nil, failure
	}
	releases = slices.Clone(releases)
	for _, r := range releases {
		if _, err := source.Satisfies("", r.Version); err != nil || r.Commit == "" {
			return nil, &ValidationError{Location: "release " + key, Err: fmt.Errorf("invalid release %+v", r)}
		}
	}
	slices.SortFunc(releases, func(a, b Release) int {
		if c := semver.Compare("v"+b.Version, "v"+a.Version); c != 0 {
			return c
		}
		return strings.Compare(a.Commit, b.Commit)
	})
	p.releases[key] = slices.Compact(releases)
	return p.releases[key], nil
}

func matches(version string, requests []Requirement) bool {
	for _, r := range requests {
		if r.Ref.Constraint == "" {
			continue
		}
		if version == "" {
			return false
		}
		ok, _ := source.Satisfies(r.Ref.Constraint, version)
		if !ok {
			return false
		}
	}
	return true
}

func constraints(requests []Requirement) []string {
	out := make([]string, len(requests))
	for i, r := range requests {
		out[i] = r.Ref.Constraint
	}
	return out
}

func (p *planner) candidates(name string, d *demand) ([]Release, error) {
	alias := keys(d.sources)[0]
	s := p.seeds[name]
	t, target := p.targets[name]
	if target {
		if r, ok := p.locked[name]; ok && !t.BoundByDependents {
			return []Release{r}, nil
		}
	}
	var pin *manifest.Resolved
	if s != nil {
		pin = s.Resolved
	}
	requests := d.requests
	if target && !t.BoundByDependents {
		// Explicit candidate policy is evaluated without dependent constraints.
		requests = []Requirement{{Ref: Ref{Constraint: s.Version}}}
	}
	if pin != nil && (!p.movable[name] || p.fixed[name]) && (!target || (t.Policy == Install && t.Constraint == "" && !s.IsDirect())) && matches(pin.Version, requests) {
		r := Release{Version: pin.Version, Commit: pin.Commit}
		if target {
			p.locked[name] = r
		}
		return []Release{r}, nil
	}
	if pin != nil && !target && !p.pinMayMove(name, d) {
		return nil, p.pinConflict(name, d)
	}
	if s != nil {
		if err := source.ValidateConstraint(s.Version); err != nil {
			return nil, &ValidationError{Location: "manifest skill " + name + " version", Err: err}
		}
	}
	overlap, _ := source.ConstraintsOverlap(constraints(requests))
	if !overlap {
		return nil, &ConflictError{Scope: p.scope, Name: name, Sources: []string{alias}, Requests: sortedRequests(d.requests), Reason: EmptyIntersection}
	}
	releases, err := p.published(alias, name)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		if s != nil && s.Version == "" && (target || s.IsDirect()) && len(d.requests) == 0 {
			if tip, ok := p.access.(TipAccess); ok {
				r, err := tip.Tip(alias, name)
				if err != nil {
					return nil, &AccessError{Source: alias, Name: name, Operation: "resolve branch tip", Err: err}
				}
				if r.Commit == "" || r.Version != "" {
					return nil, &ValidationError{Location: "branch tip " + alias + "/" + name, Err: fmt.Errorf("expected an unversioned commit")}
				}
				if target {
					p.locked[name] = r
				}
				return []Release{r}, nil
			}
		}
		return nil, &NoReleasesError{Scope: p.scope, Source: alias, Name: name, Requests: sortedRequests(d.requests)}
	}
	var out []Release
	for _, r := range releases {
		if matches(r.Version, requests) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil, &ConflictError{Scope: p.scope, Name: name, Sources: []string{alias}, Requests: sortedRequests(d.requests), Reason: NoMatchingRelease}
	}
	if target {
		r := out[0]
		if pin != nil && t.Policy == Upgrade && pin.Version != "" && semver.Compare("v"+r.Version, "v"+pin.Version) < 0 {
			return nil, &UpgradeRefusalError{Source: alias, Name: name, From: pin.Version, To: r.Version, Operation: t.Policy, Reason: "the only matching release is below the pin"}
		}
		// A new install can try an older release only to preserve a fixed pin.
		// Other explicit-target conflicts still refuse the preferred release.
		if pin == nil && t.Policy == Install {
			return out, nil
		}
		if t.BoundByDependents {
			// A dependent selected later may narrow this domain. Retain lower
			// candidates so search can reconsider it without lowering that parent.
			if pin != nil && t.Policy == Upgrade && pin.Version != "" {
				out = slices.DeleteFunc(out, func(candidate Release) bool { return semver.Compare("v"+candidate.Version, "v"+pin.Version) < 0 })
			}
			return out, nil
		}
		p.locked[name] = r
		return []Release{r}, nil
	}
	return out, nil
}

// Only pins already inconsistent with the fixed manifest graph may be repaired
// implicitly. A new dependency choice cannot move a compatible existing pin.
func (p *planner) preparePins() error {
	nodes := make(map[string]assignment)
	for _, name := range keys(p.current.Skills) {
		s := p.current.Skills[name]
		if s.Resolved == nil {
			continue
		}
		if _, target := p.targets[name]; target {
			continue
		}
		n, err := p.load(Node{Name: name, Source: s.Source, Version: s.Resolved.Version, Commit: s.Resolved.Commit})
		if err != nil {
			return err
		}
		for _, text := range n.Requires {
			ref, _ := ParseSnapshotRef(text)
			if ref.Name == name {
				return &CycleError{Path: []string{nodeRef(n), nodeRef(n)}}
			}
		}
		nodes[name] = assignment{Node: n}
	}
	graph := p.graph(nodes)
	for name, n := range nodes {
		if d := graph[name]; d != nil && !matches(n.Version, d.requests) {
			p.movable[name] = true
		}
	}
	return nil
}

func (p *planner) pinConflict(name string, d *demand) *ConflictError {
	reason := NoMatchingRelease
	if overlap, _ := source.ConstraintsOverlap(constraints(d.requests)); !overlap {
		reason = EmptyIntersection
	}
	e := &ConflictError{Scope: p.scope, Name: name, Sources: keys(d.sources), Requests: sortedRequests(d.requests), Reason: reason}
	if s := p.current.Skills[name]; s != nil && s.Resolved != nil && !p.pinMayMove(name, d) {
		e.Pinned = s.Resolved.Version
	}
	return e
}

// A changed installed parent supplies fixed requirements, like the original
// manifest graph. New dependency choices still cannot displace compatible pins.
func (p *planner) pinMayMove(name string, d *demand) bool {
	if p.fixed[name] {
		return false
	}
	if p.movable[name] {
		return true
	}
	s := p.current.Skills[name]
	if s == nil || s.Resolved == nil {
		return false
	}
	for _, r := range d.requests {
		if r.Manifest || r.Target || matches(s.Resolved.Version, []Requirement{r}) {
			continue
		}
		parent := p.current.Skills[r.Requester]
		if parent != nil && parent.Resolved != nil && parent.Resolved.Commit != r.Commit {
			return true
		}
	}
	return false
}
