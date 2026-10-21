package dependency

import (
	"errors"
	"maps"
	"slices"
)

type demand struct {
	sources  map[string]bool
	requests []Requirement
	origins  []Requirement
}

// Assignments are immutable within a branch. Backtracking discards the whole
// branch, including every outgoing edge introduced by its candidates.
type assignment struct{ Node }

const maxSearchStates = 10000

// searchFailure carries the decisions that can change this failure. As a
// decision exhausts its candidates it substitutes the decisions constraining
// its domain, jumping over unrelated decisions. Fatal failures never backtrack.
type searchFailure struct {
	err    error
	causes map[string]bool
	fatal  bool
}

func (f *searchFailure) Error() string { return f.err.Error() }
func (f *searchFailure) Unwrap() error { return f.err }

func failureFor(err error, names ...string) *searchFailure {
	f := &searchFailure{err: err, causes: make(map[string]bool)}
	for _, name := range names {
		f.causes[name] = true
	}
	var access *AccessError
	var validation *ValidationError
	var unknown *UnknownSourceError
	var noReleases *NoReleasesError
	var budget *SearchBudgetError
	f.fatal = errors.As(err, &access) || errors.As(err, &validation) || errors.As(err, &unknown) || errors.As(err, &noReleases) || errors.As(err, &budget)
	return f
}

func demandCauses(name string, d *demand) []string {
	names := []string{name}
	for _, r := range d.origins {
		if !r.Manifest && !r.Target {
			names = append(names, r.Requester)
		}
	}
	return names
}

// graph is rebuilt solely from the current assignments. No edge from an
// abandoned candidate survives, and non-manifest descendants disappear with it.
func (p *planner) graph(selected map[string]assignment) map[string]*demand {
	graph := make(map[string]*demand)
	add := func(r Requirement) {
		d := graph[r.Ref.Name]
		if d == nil {
			d = &demand{sources: make(map[string]bool)}
			graph[r.Ref.Name] = d
		}
		d.sources[r.Ref.Source] = true
		d.origins = append(d.origins, r)
		if r.Ref.Constraint != "" {
			d.requests = append(d.requests, r)
		}
	}
	queue := keys(p.seeds)
	for _, name := range queue {
		s := p.seeds[name]
		r := Requirement{Ref: Ref{Source: s.Source, Name: name, Constraint: s.Version}, Requester: name, Source: s.Source, Manifest: true, Index: -1}
		if _, ok := p.targets[name]; ok {
			r.Target, r.Manifest = true, false
		}
		if s.Resolved != nil {
			r.Version, r.Commit = s.Resolved.Version, s.Resolved.Commit
		}
		add(r)
	}
	visited := make(map[string]bool)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if visited[name] {
			continue
		}
		visited[name] = true
		n, ok := selected[name]
		if !ok {
			continue
		}
		for i, text := range n.Requires {
			ref, _ := ParseRef(text) // validated and normalized by load
			if indexes := p.indexes[nodeRef(n.Node)]; indexes != nil {
				i = indexes[text]
			}
			add(incoming(n.Node, ref, i))
			queue = append(queue, ref.Name)
		}
	}
	return graph
}

func (p *planner) collision(name string, d *demand) error {
	if len(d.sources) < 2 {
		return nil
	}
	return &ConflictError{Scope: p.scope, Name: name, Sources: keys(d.sources), Requests: sortedRequests(d.origins), Reason: SourceCollision}
}

func (p *planner) search(selected map[string]assignment) (map[string]assignment, error) {
	if p.explored >= maxSearchStates {
		return nil, failureFor(&SearchBudgetError{Limit: maxSearchStates, Explored: p.explored, Skills: keys(p.graph(selected))})
	}
	p.explored++
	graph := p.graph(selected)
	if err := cycle(selected); err != nil {
		var names []string
		for _, step := range err.(*CycleError).Path {
			ref, _ := ParseRef(step)
			names = append(names, ref.Name)
		}
		return nil, failureFor(err, names...)
	}
	// Source identity can never be repaired by assigning the colliding name.
	// Check it before expanding any unrelated root or dependency.
	for _, name := range keys(graph) {
		d := graph[name]
		if err := p.collision(name, d); err != nil {
			f := failureFor(err, demandCauses(name, d)...)
			fixed := true
			for _, r := range d.origins {
				if r.Manifest || r.Target {
					continue
				}
				if _, target := p.targets[r.Requester]; target {
					continue
				}
				s := p.current.Skills[r.Requester]
				if s == nil || s.Resolved == nil || p.movable[r.Requester] {
					fixed = false
				}
			}
			f.fatal = fixed
			return nil, f
		}
	}
	pending := make([]string, 0)
	for _, name := range keys(graph) {
		if _, ok := selected[name]; !ok {
			pending = append(pending, name)
		}
	}
	priority := func(name string) int {
		if t, ok := p.targets[name]; ok {
			if t.BoundByDependents {
				// Expand parents before selecting within their ranges.
				return 4
			}
			return 0
		}
		if s := p.seeds[name]; s != nil {
			// Expand a necessarily changed pin before compatible pins so its
			// new requirements determine whether those pins need to move.
			if s.Resolved != nil && !matches(s.Resolved.Version, graph[name].requests) && p.pinMayMove(name, graph[name]) {
				return 1
			}
			return 2
		}
		return 3
	}
	slices.SortStableFunc(pending, func(a, b string) int { return priority(a) - priority(b) })
	// Expand fixed roots first so errors retain every manifest requester and
	// explicit-target blockers take precedence over failures in their closure.
	expandingSeeds := len(pending) > 0 && priority(pending[0]) < 3
	if !expandingSeeds {
		for _, name := range keys(graph) {
			d := graph[name]
			n, ok := selected[name]
			if !ok || matches(n.Version, d.requests) {
				continue
			}
			if n.Version == "" {
				return nil, failureFor(&NoReleasesError{Scope: p.scope, Source: n.Source, Name: name, Requests: sortedRequests(d.requests)})
			}
			if t, target := p.targets[name]; target {
				var blocking []Requirement
				for _, r := range d.requests {
					if !matches(n.Version, []Requirement{r}) {
						blocking = append(blocking, r)
					}
				}
				from := ""
				if s := p.current.Skills[name]; s != nil && s.Resolved != nil {
					from = s.Resolved.Version
					if from == "" {
						from = s.Resolved.Commit
					}
				}
				return nil, failureFor(&UpgradeRefusalError{Source: n.Source, Name: name, From: from, To: n.Version, Operation: t.Policy, Blocking: sortedRequests(blocking)}, demandCauses(name, d)...)
			}
			return nil, failureFor(p.pinConflict(name, d), demandCauses(name, d)...)
		}
		// A failed domain must be handled now, before branching on unrelated names.
		for _, name := range pending {
			if _, err := p.candidates(name, graph[name]); err != nil {
				return nil, failureFor(err, demandCauses(name, graph[name])...)
			}
		}
	}
	if len(pending) == 0 {
		return selected, nil
	}
	name := pending[0]
	d := graph[name]
	candidates, err := p.candidates(name, d)
	if err != nil {
		return nil, failureFor(err, demandCauses(name, d)...)
	}
	var first *searchFailure
	causes := make(map[string]bool)
	for _, r := range candidates {
		n, err := p.load(Node{Name: name, Source: keys(d.sources)[0], Version: r.Version, Commit: r.Commit})
		loadFailed := err != nil
		if err == nil {
			branch := maps.Clone(selected)
			branch[name] = assignment{Node: n}
			result, branchErr := p.search(branch)
			if branchErr == nil {
				return result, nil
			}
			err = branchErr
		}
		var f *searchFailure
		if !errors.As(err, &f) {
			f = failureFor(err, name)
		}
		// Invalid frontmatter belongs to this release, unlike invalid dependency
		// references and malformed release lists, which remain fatal input errors.
		var candidate *candidateValidationError
		if loadFailed && errors.As(err, &candidate) {
			f.fatal = false
			f.causes[name] = true
		}
		if f.fatal {
			return nil, f
		}
		if !f.causes[name] {
			return nil, f
		}
		if t, target := p.targets[name]; target && t.Policy == Install {
			var conflict *ConflictError
			if !errors.As(err, &conflict) || conflict.Pinned == "" {
				return nil, f
			}
		}
		if first == nil {
			first = f
		}
		for cause := range f.causes {
			if cause != name {
				causes[cause] = true
			}
		}
	}
	for _, cause := range demandCauses(name, d) {
		if cause != name {
			causes[cause] = true
		}
	}
	return nil, &searchFailure{err: first.err, causes: causes}
}

func cycle(nodes map[string]assignment) error {
	visited := make(map[string]bool)
	active := make(map[string]int)
	var stack []string
	var visit func(string) error
	visit = func(name string) error {
		if _, ok := nodes[name]; !ok {
			return nil
		}
		if i, ok := active[name]; ok {
			path := make([]string, 0, len(stack)-i+1)
			for _, step := range append(slices.Clone(stack[i:]), name) {
				path = append(path, nodeRef(nodes[step].Node))
			}
			return &CycleError{Path: path}
		}
		if visited[name] {
			return nil
		}
		active[name] = len(stack)
		stack = append(stack, name)
		for _, text := range nodes[name].Requires {
			r, _ := ParseRef(text)
			if err := visit(r.Name); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		delete(active, name)
		visited[name] = true
		return nil
	}
	for _, name := range keys(nodes) {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func (p *planner) plan(selected map[string]Node) *Plan {
	graphNodes := make([]Node, 0, len(selected))
	for _, name := range keys(selected) {
		n := selected[name]
		if s := p.seeds[name]; s != nil {
			n.Direct = s.IsDirect()
		}
		if t, ok := p.targets[name]; ok && !t.PreserveDirect {
			n.Direct = true
		}
		graphNodes = append(graphNodes, n)
	}
	graph := AnalyzeGraph(graphNodes)
	plan := &Plan{Nodes: []Node{}, Orphans: []string{}, Diff: Diff{Added: []Node{}, Changed: []Node{}, Unchanged: []Node{}}}
	for _, name := range keys(selected) {
		n := selected[name]
		if s := p.seeds[name]; s != nil {
			n.Constraint, n.Direct = s.Version, s.IsDirect()
		}
		if t, ok := p.targets[name]; ok && !t.PreserveDirect {
			n.Direct = true
		}
		n.Via = graph.Via[name]
		if n.Via == nil {
			n.Via = []string{}
		}
		plan.Nodes = append(plan.Nodes, n)
		s := p.current.Skills[name]
		if s == nil {
			plan.Diff.Added = append(plan.Diff.Added, n)
			continue
		}
		same := s.Source == n.Source && s.Version == n.Constraint && s.IsDirect() == n.Direct && slices.Equal(s.Via, n.Via) && s.Resolved != nil
		if same {
			same = s.Resolved.Version == n.Version && s.Resolved.Commit == n.Commit && s.Resolved.Requires != nil && slices.Equal(*s.Resolved.Requires, n.Requires)
		}
		if same {
			plan.Diff.Unchanged = append(plan.Diff.Unchanged, n)
		} else {
			plan.Diff.Changed = append(plan.Diff.Changed, n)
		}
	}
	plan.Orphans = graph.Orphans
	return plan
}

// GraphInfo describes a pinned graph without resolving versions or accessing sources.
// Via and Orphans are sorted and unique. Cycle is the first deterministic cycle.
type GraphInfo struct {
	Via     map[string][]string
	Orphans []string
	Cycle   error
}

// AnalyzeGraph reuses planner traversal for diagnostics, including disconnected
// subtrees. Missing nodes and malformed references are skipped; callers diagnose
// those separately. Inputs are never modified.
func AnalyzeGraph(nodes []Node) GraphInfo {
	selected := make(map[string]assignment, len(nodes))
	for _, n := range nodes {
		raw := n.Requires
		n.Requires = nil
		for _, text := range raw {
			if r, err := ParseRef(text); err == nil {
				n.Requires = append(n.Requires, r.WithDefaultSource(n.Source).String())
			}
		}
		slices.Sort(n.Requires)
		n.Requires = slices.Compact(n.Requires)
		selected[n.Name] = assignment{Node: n}
	}
	info := GraphInfo{Via: make(map[string][]string), Orphans: []string{}, Cycle: cycle(selected)}
	for _, name := range keys(selected) {
		for _, text := range selected[name].Requires {
			r, _ := ParseRef(text)
			info.Via[r.Name] = append(info.Via[r.Name], name)
		}
	}
	for name, parents := range info.Via {
		info.Via[name] = slices.Compact(parents)
	}
	reachable := make(map[string]bool)
	var mark func(string)
	mark = func(name string) {
		if reachable[name] {
			return
		}
		if _, ok := selected[name]; !ok {
			return
		}
		reachable[name] = true
		for _, text := range selected[name].Requires {
			r, _ := ParseRef(text)
			mark(r.Name)
		}
	}
	for _, name := range keys(selected) {
		n := selected[name]
		if n.Direct {
			mark(n.Name)
		}
	}
	for _, name := range keys(selected) {
		n := selected[name]
		if !reachable[n.Name] {
			info.Orphans = append(info.Orphans, n.Name)
		}
	}
	return info
}
