package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

// installSources separates selecting releases from reading exact commits. Exact
// reads use the cache first, including when the original repository is gone.
type installSources struct {
	current    *manifest.Manifest
	restore    bool
	opened     map[string]*source.Source
	paths      paths
	targets    map[string]bool
	legacy     map[string]bool
	unknown    map[string]bool // legacy pins outside the request whose tree cannot be read
	unverified map[string]bool // legacy parents without authoritative installed requirements
	fetched    map[string]bool
	releases   map[string][]dependency.Release
}

// Pin constraints belong to the planner, allowing release search to backtrack
// instead of treating a restore refusal as a Git access failure. The planner
// supplies the actual target list for both install and upgrade.
func (a *installSources) PinConstraints(targets []dependency.Target) (map[string]bool, error) {
	explicit := map[string]bool{}
	for _, target := range targets {
		explicit[target.Name] = true
	}
	fixed := map[string]bool{}
	for _, name := range sortedNames(a.current.Skills) {
		s := a.current.Skills[name]
		if s == nil || s.Resolved == nil {
			continue
		}
		if a.restore {
			fixed[name] = true
		}
		if !explicit[name] && s.Version != "" && source.ValidateConstraint(s.Version) == nil {
			ok, _ := source.Satisfies(s.Version, s.Resolved.Version)
			if !ok {
				return nil, &dependency.ValidationError{Location: fmt.Sprintf("skills.%s.version = %q", name, s.Version), Err: fmt.Errorf("excludes stale pin %s; clear skills.%s.resolved (the resolved block) and run kitt install again", versionLabel(s.Resolved.Version, s.Resolved.Commit), name)}
			}
		}
	}
	return fixed, nil
}

func (a *installSources) Releases(alias, name string) ([]dependency.Release, error) {
	key := alias + "/" + name
	if releases, ok := a.releases[key]; ok {
		return slices.Clone(releases), nil
	}
	s, err := a.refresh(alias)
	if err != nil {
		return nil, err
	}
	pins, err := s.PublishedReleases(name)
	if err != nil {
		return nil, fmt.Errorf("list releases from source %s: %w", alias, err)
	}
	out := make([]dependency.Release, len(pins))
	for i, pin := range pins {
		out[i] = dependency.Release{Version: pin.Version, Commit: pin.Commit}
	}
	if a.releases == nil {
		a.releases = map[string][]dependency.Release{}
	}
	a.releases[key] = out
	return slices.Clone(out), nil
}

func (a *installSources) Tip(alias, name string) (dependency.Release, error) {
	s, err := a.refresh(alias)
	if err != nil {
		return dependency.Release{}, err
	}
	pin, err := s.BranchTip(name)
	return dependency.Release{Version: pin.Version, Commit: pin.Commit}, err
}

func (a *installSources) exact(alias, commit string) (*source.Source, error) {
	s := a.opened[alias]
	if s == nil {
		url, ok := a.current.Sources[alias]
		if !ok {
			return nil, fmt.Errorf("unknown source %s", alias)
		}
		var err error
		s, err = source.Open(alias, url)
		if err != nil {
			return nil, &dependency.AccessError{Source: alias, Operation: "open source", Err: fmt.Errorf("open source %s: %w", alias, err)}
		}
		a.opened[alias] = s
	}
	if commit != "" && !s.HasCommit(commit) {
		if _, err := a.refresh(alias); err != nil {
			return nil, err
		}
		if !s.HasCommit(commit) {
			return nil, &dependency.AccessError{Source: alias, Operation: "read commit", Err: fmt.Errorf("commit %s not found in %s", shortCommit(commit), alias)}
		}
	}
	return s, nil
}

func (a *installSources) refresh(alias string) (*source.Source, error) {
	s, err := a.exact(alias, "")
	if err != nil {
		return nil, err
	}
	if a.fetched == nil {
		a.fetched = map[string]bool{}
	}
	if !a.fetched[alias] {
		if sourceFetchTestHook != nil {
			sourceFetchTestHook()
		}
		if err := s.Fetch(); err != nil {
			return nil, &dependency.AccessError{Source: alias, Operation: "fetch source", Err: fmt.Errorf("fetch source %s: %w", alias, err)}
		}
		a.fetched[alias] = true
	}
	return s, nil
}

func (a *installSources) Requires(alias, name, commit string) ([]string, error) {
	if err := preflightDestination(a.paths, name); err != nil {
		return nil, err
	}
	s, err := a.exact(alias, commit)
	if err != nil {
		return nil, err
	}
	raw, err := s.Requires(name, commit)
	var metadata *source.MetadataError
	if errors.As(err, &metadata) {
		return nil, &dependency.ValidationError{Location: name + "/SKILL.md", Err: err}
	}
	if err != nil {
		return nil, &dependency.AccessError{Source: alias, Name: name, Commit: commit, Operation: "read requirements", Err: err}
	}
	return raw, nil
}

func planAndInstall(opts Options, ref *string) (err error) {
	defer func() { err = installError(err) }()
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := load(p)
	if err != nil {
		return err
	}
	if err := validateManifestNames(m); err != nil {
		return err
	}
	var targets []dependency.Target
	if ref != nil {
		t, err := installTarget(m, p, *ref)
		if err != nil {
			return err
		}
		targets = []dependency.Target{t}
	}
	if err := prepareOwnership(&p, m); err != nil {
		return validationError(err)
	}
	if len(targets) > 0 {
		if err := p.installed.check(targets[0].Name); err != nil {
			return err
		}
	}
	access := &installSources{current: m, restore: ref == nil, opened: map[string]*source.Source{}, paths: p}
	current, err := planningManifest(p, m, access, targets...)
	if err != nil {
		return err
	}
	plan, err := buildDependencyPlan(p, current, targets, access, "")
	if err != nil {
		return err
	}
	preserveUnknownRequirementPins(m, plan, access.unknown)
	out := outputOf(opts.Out)
	var verify map[string]bool
	if len(targets) > 0 {
		verify = map[string]bool{targets[0].Name: true}
	}
	staged, err := stageAndApplyPlan(p, m, plan, access, ref == nil, verify, out)
	if err != nil {
		return err
	}
	for _, n := range plan.Nodes {
		verb := "ok"
		if len(targets) > 0 && n.Name == targets[0].Name {
			verb = "added"
			if old := m.Skills[n.Name]; old != nil && !old.IsDirect() && old.Resolved != nil && old.Resolved.Commit == n.Commit && staged[n.Name].dir == "" {
				verb = "up to date"
			}
		} else if s := m.Skills[n.Name]; s == nil || s.Resolved == nil {
			verb = "pinned"
		} else if staged[n.Name].dir != "" {
			verb = "installed"
		} else if ref != nil {
			continue
		}
		fmt.Fprintf(out, "%s %s %s\n", verb, n.Name, versionLabel(n.Version, n.Commit))
	}
	for _, name := range plan.Orphans {
		fmt.Fprintf(out, "orphaned %s (retained)\n", name)
	}
	return nil
}

// prepareOwnership collects migration/adoption in memory until the plan applies.
func prepareOwnership(p *paths, m *manifest.Manifest) error {
	var err error
	p.installed, err = readInstalledWithMode(*p, m, true)
	if err != nil {
		return err
	}
	// Preserve existing ownership diagnostics, and collect adoption only in memory.
	for _, name := range sortedNames(m.Skills) {
		if err := p.installed.adopt(name, m.Skills[name]); err != nil {
			return err
		}
		if err := p.installed.check(name); err != nil {
			return err
		}
	}
	return nil
}

// stageAndApplyPlan is the shared install/restore/upgrade application path.
// Application starts only after every graph and staging check succeeds.
func stageAndApplyPlan(p paths, m *manifest.Manifest, plan *dependency.Plan, access *installSources, prune bool, verify map[string]bool, out io.Writer) (staged map[string]stagedSkill, err error) {
	validating := true
	defer func() {
		if err != nil && validating {
			err = validationError(err)
		}
	}()
	if err := preflightInstall(p, plan); err != nil {
		return nil, err
	}
	// Sweep failures are warnings and must not abort the run.
	_ = sweepStaging(p.skillsDir, time.Now())
	staged, cleanup, err := stagePlan(p, m, plan, access, prune, verify)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	next := plannedManifest(m, plan, staged, access.unknown)
	tx, err := newApplyTransaction(p)
	if err != nil {
		return nil, err
	}
	validating = false
	committed := false
	defer func() {
		if err != nil && !committed {
			if rollbackErr := tx.rollback(); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			} else {
				err = &dependency.PlanError{Err: err, ManagedStateUntouched: true}
			}
		}
	}()
	// Buffer success messages until the complete transaction commits.
	var report bytes.Buffer
	p.installed.deferred = false
	if p.installed.dirty {
		if err = p.installed.save(); err != nil {
			return nil, err
		}
	}
	if err = tx.prepareFinish(p, sortedNames(next.Skills), prune, &report); err != nil {
		return nil, err
	}
	if err = applyStaged(p, plan, staged, tx); err != nil {
		return nil, err
	}
	if err = applyStep("finish", p.root); err != nil {
		return nil, err
	}
	if err = finish(p, sortedNames(next.Skills), prune, &report); err != nil {
		return nil, err
	}
	if p.installed.dirty {
		if err = applyStep("ownership", filepath.Join(p.skillsDir, installedFile)); err != nil {
			return nil, err
		}
		if err = p.installed.save(); err != nil {
			return nil, err
		}
	}
	if !reflect.DeepEqual(m, next) {
		if err = applyStep("manifest", p.manifest); err != nil {
			return nil, err
		}
		if err = next.Save(p.manifest); err != nil {
			return nil, err
		}
	}
	committed = true
	if removeErr := removeTree(tx.root); removeErr != nil {
		// The install is committed. A locked backup must not hide the report
		// or fail the command; rollback is no longer possible.
		fmt.Fprintf(os.Stderr, "warning: installation committed; remove backup directory %s manually: %v\n", tx.root, removeErr)
	}
	_, err = io.Copy(out, &report)
	return staged, err
}

func installTarget(m *manifest.Manifest, p paths, text string) (dependency.Target, error) {
	nameAndSource, constraint, _ := strings.Cut(text, "@")
	alias, name, qualified := strings.Cut(nameAndSource, "/")
	if !qualified {
		name, alias = alias, ""
	}
	if err := skill.ValidName(name); err != nil {
		return dependency.Target{}, err
	}
	if err := source.ValidateConstraint(constraint); err != nil {
		return dependency.Target{}, err
	}
	r := dependency.Ref{Source: alias, Name: name, Constraint: constraint}
	if strings.HasPrefix(r.Name, "kitt-") {
		return dependency.Target{}, fmt.Errorf("skill names starting with kitt- are reserved")
	}
	if r.Source == "" {
		if s := m.Skills[r.Name]; s != nil {
			r.Source = s.Source
		} else {
			switch len(m.Sources) {
			case 0:
				return dependency.Target{}, fmt.Errorf("no source in %s: add a [sources] table", p.manifest)
			case 1:
				for alias := range m.Sources {
					r.Source = alias
				}
			default:
				return dependency.Target{}, fmt.Errorf("several sources: use <source>/<skill>")
			}
		}
	}
	if _, ok := m.Sources[r.Source]; !ok {
		return dependency.Target{}, fmt.Errorf("unknown source %s", r.Source)
	}
	return dependency.Target{Name: r.Name, Source: r.Source, Constraint: r.Constraint, Policy: dependency.Install}, nil
}

// Populate legacy snapshots for planning, without necessarily persisting them.
// Only hash-verified installed content is authoritative. Otherwise read the pin.
func planningManifest(p paths, m *manifest.Manifest, access *installSources, targets ...dependency.Target) (*manifest.Manifest, error) {
	access.targets = map[string]bool{}
	access.legacy = map[string]bool{}
	access.unknown = map[string]bool{}
	access.unverified = map[string]bool{}
	for _, target := range targets {
		access.targets[target.Name] = true
	}
	if access.restore || targets != nil {
		if _, err := access.PinConstraints(targets); err != nil {
			return nil, err
		}
	}
	current := &manifest.Manifest{Sources: maps.Clone(m.Sources), Skills: map[string]*manifest.Skill{}}
	for _, name := range sortedNames(m.Skills) {
		s := m.Skills[name]
		if s == nil {
			return nil, fmt.Errorf("skill %s: nil manifest entry", name)
		}
		if err := source.ValidateConstraint(s.Version); err != nil && s.Resolved == nil {
			return nil, fmt.Errorf("skills.%s.version: %w", name, err)
		}

		copy := *s
		current.Skills[name] = &copy
		if s.Resolved == nil {
			continue
		}
		pin := *s.Resolved
		copy.Resolved = &pin
		if pin.Requires != nil {
			continue
		}
		// Verified legacy trees need no source IO. Non-kitt requires metadata
		// outside the requested closure is unknown, rather than a fatal input.
		raw, matched, err := installedRequirements(p, s, name)
		if !access.restore && (!matched || err != nil) {
			access.unverified[name] = true
		}
		if err != nil {
			if (!access.targets[name] || access.restore) && matched && strings.Contains(err.Error(), "metadata.requires") {
				fmt.Fprintf(os.Stderr, "warning: requirements for %s are unknown: %v\n", name, err)
			} else {
				return nil, err
			}
		} else if matched {
			pin.Requires = &raw
			continue
		}
		if err != nil || !access.restore && !access.targets[name] {
			// A missing or modified unrelated tree is not authoritative. If the
			// requested closure reaches it, buildDependencyPlan loads the pin.
			access.legacy[name] = true
			access.unknown[name] = true
			empty := []string{}
			pin.Requires = &empty
			continue
		}
		raw, err = pinnedRequirements(p, s, name, access)
		if err != nil {
			return nil, err
		}
		if raw == nil {
			raw = []string{}
		}
		pin.Requires = &raw
	}
	if access.restore {
		for _, name := range sortedNames(current.Skills) {
			s := current.Skills[name]
			if s.Resolved == nil {
				continue
			}
			for _, text := range *s.Resolved.Requires {
				r, err := dependency.ParseSnapshotRef(text)
				if err != nil {
					return nil, fmt.Errorf("skills.%s.resolved.requires: %w", name, err)
				}
				r = r.WithDefaultSource(s.Source)
				if _, known := m.Sources[r.Source]; known && current.Skills[r.Name] == nil {
					return nil, validationError(fmt.Errorf("incomplete manifest: pinned %s/%s requires %q, but skills.%s is missing; restore the missing manifest entry", s.Source, name, r.String(), r.Name))
				}
			}
		}
	}
	return current, nil
}

// Load legacy metadata only when the requested closure actually reaches it.
func buildDependencyPlan(p paths, current *manifest.Manifest, targets []dependency.Target, access *installSources, command string) (*dependency.Plan, error) {
	for {
		plan, err := dependency.BuildPlan(dependency.Scope{Global: p.global, ManifestPath: p.manifest, Command: command}, current, targets, access)
		if err != nil {
			return nil, err
		}
		nodes := map[string]dependency.Node{}
		for _, n := range plan.Nodes {
			nodes[n.Name] = n
		}
		changed := false
		seen := map[string]bool{}
		var visit func(string) error
		visit = func(name string) error {
			if seen[name] {
				return nil
			}
			seen[name] = true
			if access.legacy[name] {
				s := current.Skills[name]
				raw, err := pinnedRequirements(p, s, name, access)
				if err != nil {
					return err
				}
				s.Resolved.Requires = &raw
				delete(access.legacy, name)
				delete(access.unknown, name)
				changed = true
			}
			for _, text := range nodes[name].Requires {
				r, _ := dependency.ParseSnapshotRef(text)
				if err := visit(r.Name); err != nil {
					return err
				}
			}
			return nil
		}
		for _, target := range targets {
			if err := visit(target.Name); err != nil {
				return nil, err
			}
		}
		if !changed {
			if err := verifyUnknownParents(access.current, plan, access.unverified); err != nil {
				return nil, validationError(err)
			}
			return plan, nil
		}
	}
}

// installedRequirements reads requirements from a hash-verified skill directory.
// matched is false when the tree is missing or modified.
func installedRequirements(p paths, s *manifest.Skill, name string) (raw []string, matched bool, err error) {
	matched, err = hashMatches(filepath.Join(p.skillsDir, name), s.Resolved.Hash)
	if err != nil || !matched {
		return nil, matched, err
	}
	meta, err := source.ReadSkillMetadata(filepath.Join(p.skillsDir, name), name)
	if err != nil {
		return nil, true, validationError(err)
	}
	raw, err = dependency.NormalizeRequirements(meta.Requires, s.Source, false)
	return raw, true, err
}

func pinnedRequirements(p paths, s *manifest.Skill, name string, access *installSources) ([]string, error) {
	match, err := hashMatches(filepath.Join(p.skillsDir, name), s.Resolved.Hash)
	if err != nil {
		return nil, err
	}
	if match {
		meta, err := source.ReadSkillMetadata(filepath.Join(p.skillsDir, name), name)
		if err != nil {
			return nil, validationError(err)
		}
		return dependency.NormalizeRequirements(meta.Requires, s.Source, false)
	}
	raw, err := access.Requires(s.Source, name, s.Resolved.Commit)
	if err != nil {
		return nil, err
	}
	return dependency.NormalizeRequirements(raw, s.Source, false)
}

func plannedManifest(m *manifest.Manifest, plan *dependency.Plan, staged map[string]stagedSkill, unknown map[string]bool) *manifest.Manifest {
	next := &manifest.Manifest{Sources: maps.Clone(m.Sources), Skills: map[string]*manifest.Skill{}}
	snapshots := false
	for _, n := range plan.Nodes {
		if unknown[n.Name] {
			continue
		}
		snapshots = snapshots || len(n.Requires) > 0
	}
	// Retained transitive entries need their graph keys even after the last
	// edge disappears. Unknown parents may also hide recorded via edges.
	keepGraph := snapshots
	for _, n := range plan.Nodes {
		if !n.Direct || len(n.Via) > 0 {
			keepGraph = true
			break
		}
	}
	for _, n := range plan.Nodes {
		s := &manifest.Skill{Source: n.Source, Version: n.Constraint, Via: slices.Clone(n.Via)}
		old := m.Skills[n.Name]
		if keepGraph && !n.Direct {
			s.Direct = new(false)
		} else if keepGraph && old != nil && old.Direct != nil && *old.Direct {
			s.Direct = new(true)
		}
		if !keepGraph || len(s.Via) == 0 {
			s.Via = nil
		}
		if n.Direct && s.Version == "" && n.Version != "" && (old == nil || old.Resolved == nil) {
			s.Version = "^" + n.Version
		}
		s.Resolved = &manifest.Resolved{Version: n.Version, Commit: n.Commit, Hash: staged[n.Name].hash}
		writeRequires := keepGraph && !unknown[n.Name]
		if !writeRequires && len(unknown) > 0 && !unknown[n.Name] && old != nil && old.Resolved != nil && old.Resolved.Requires != nil {
			writeRequires = true
		}
		if writeRequires {
			raw := slices.Clone(n.Requires)
			if raw == nil {
				raw = []string{}
			}
			s.Resolved.Requires = &raw
		}
		next.Skills[n.Name] = s
	}
	return next
}

// preserveUnknownRequirementPins puts recorded parents back when their
// requirements could not be read. An unknown pin is not evidence that an
// edge disappeared, so it must not clear via or mark the child orphaned.
func preserveUnknownRequirementPins(original *manifest.Manifest, plan *dependency.Plan, unknown map[string]bool) {
	if len(unknown) == 0 || plan == nil {
		return
	}
	index := make(map[string]int, len(plan.Nodes))
	for i, n := range plan.Nodes {
		index[n.Name] = i
	}
	for name, s := range original.Skills {
		if s == nil {
			continue
		}
		i, ok := index[name]
		if !ok {
			continue
		}
		n := &plan.Nodes[i]
		if unknown[name] {
			n.Requires = nil
		}
		for _, parent := range s.Via {
			if unknown[parent] && !slices.Contains(n.Via, parent) {
				n.Via = append(n.Via, parent)
			}
		}
		slices.Sort(n.Via)
		n.Via = slices.Compact(n.Via)
	}
	plan.Orphans = reachableOrphans(plan.Nodes)
}

// A recorded edge from an unverifiable legacy parent still protects its child.
// Restoring that parent establishes an authoritative requirements snapshot.
func verifyUnknownParents(original *manifest.Manifest, plan *dependency.Plan, unverified map[string]bool) error {
	for _, n := range plan.Nodes {
		s := original.Skills[n.Name]
		if s == nil || s.Resolved == nil || s.Source == n.Source && s.Resolved.Commit == n.Commit {
			continue
		}
		for _, parent := range s.Via {
			if unverified[parent] {
				return fmt.Errorf("cannot verify that %s accepts %s %s; run `kitt install` to restore %s first", parent, n.Name, versionLabel(n.Version, n.Commit), parent)
			}
		}
	}
	return nil
}

func reachableOrphans(nodes []dependency.Node) []string {
	children := make(map[string][]string, len(nodes))
	present := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		present[n.Name] = true
		for _, parent := range n.Via {
			children[parent] = append(children[parent], n.Name)
		}
	}
	seen := map[string]bool{}
	var walk func(string)
	walk = func(name string) {
		if seen[name] || !present[name] {
			return
		}
		seen[name] = true
		for _, child := range children[name] {
			walk(child)
		}
	}
	for _, n := range nodes {
		if n.Direct {
			walk(n.Name)
		}
	}
	var orphans []string
	for _, n := range nodes {
		if !seen[n.Name] {
			orphans = append(orphans, n.Name)
		}
	}
	slices.Sort(orphans)
	return orphans
}

func validationError(err error) error {
	if err == nil {
		return nil
	}
	var access *dependency.AccessError
	if errors.As(err, &access) {
		return err
	}
	return &dependency.ValidationError{Err: err}
}

// Command prefixes belong to the CLI. Git and configuration failures retain
// their established diagnostics, without a planning-state claim.
func installError(err error) error {
	if err == nil {
		return nil
	}
	var plan *dependency.PlanError
	if errors.As(err, &plan) {
		return err
	}
	var access *dependency.AccessError
	if errors.As(err, &access) {
		for {
			var inner *dependency.AccessError
			if !errors.As(access.Err, &inner) {
				break
			}
			access = inner
		}
		if access.Name != "" && access.Commit != "" {
			return fmt.Errorf("%s %s/%s at %s: %w", access.Operation, access.Source, access.Name, shortCommit(access.Commit), access.Err)
		}
		return access.Err
	}
	var validation *dependency.ValidationError
	if errors.As(err, &validation) {
		return &dependency.PlanError{Err: err, ManagedStateUntouched: true}
	}
	return err
}

// Counts actual source refreshes in adapter integration tests.
var sourceFetchTestHook func()
