package dependency

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/anthony-cordani/kitt/internal/source"
)

type demand struct {
	sources  map[string]bool
	requests []Requirement
	origins  []Requirement
}

// Constraints records the predicates used when a candidate was selected. If an
// abandoned parent removes one, reconsider the highest candidate under the new
// graph. Compatible original pins remain fixed regardless of relaxed ranges.
type assignment struct {
	Node
	Constraints []string
}

// A repeated partial assignment rejects only this candidate branch. It must not
// be treated as malformed input, which would prematurely abort backtracking.
var errRevisited = errors.New("dependency candidate state already examined")

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
	// JSON sorts map keys and quotes all fields, avoiding ambiguous state keys.
	state, _ := json.Marshal(selected)
	key := string(state)
	if err, ok := p.failed[key]; ok {
		return nil, err
	}
	graph := p.graph(selected)
	if p.active[key] {
		return nil, errRevisited
	}
	p.active[key] = true
	defer delete(p.active, key)
	finish := func(err error) (map[string]assignment, error) {
		p.failed[key] = err
		return nil, err
	}

	// Drop unreachable assignments, then invalid candidates and all their
	// outgoing edges before selecting replacements. Existing seeds are retained.
	next := maps.Clone(selected)
	for _, name := range keys(selected) {
		n := selected[name]
		d, reachable := graph[name]
		if !reachable {
			delete(next, name)
			continue
		}
		if _, target := p.targets[name]; !target && len(d.sources) == 1 {
			if !matches(n.Version, d.requests) {
				delete(next, name)
				continue
			}
			// A provisional constraint may have forced a pin to move. If that
			// constraint disappeared with an abandoned candidate, restore the
			// now-compatible original pin rather than keeping its replacement.
			if s := p.current.Skills[name]; s != nil && s.Resolved != nil && s.Source == n.Source && matches(s.Resolved.Version, d.requests) {
				if n.Commit != s.Resolved.Commit {
					delete(next, name)
				}
				continue
			}
			current := constraints(d.requests)
			for _, old := range n.Constraints {
				if !slices.Contains(current, old) {
					delete(next, name)
					break
				}
			}
		}
	}
	if len(next) != len(selected) {
		result, err := p.search(next)
		if err != nil {
			if errors.Is(err, errRevisited) {
				if cycleErr := cycle(selected); cycleErr != nil {
					err = cycleErr
				} else {
					for _, name := range keys(selected) {
						if d := graph[name]; d != nil && !matches(selected[name].Version, d.requests) {
							overlap, _ := source.ConstraintsOverlap(constraints(d.requests))
							reason := EmptyIntersection
							if overlap {
								reason = NoMatchingRelease
							}
							err = &ConflictError{Scope: p.scope, Name: name, Sources: keys(d.sources), Requests: sortedRequests(d.requests), Reason: reason}
							break
						}
					}
				}
			}
			return finish(err)
		}
		return result, nil
	}

	pending := make([]string, 0)
	for _, name := range keys(graph) {
		if _, ok := selected[name]; !ok {
			pending = append(pending, name)
		}
	}
	priority := func(name string) int {
		if _, ok := p.targets[name]; ok {
			return 0
		}
		if _, ok := p.seeds[name]; ok {
			return 1
		}
		return 2
	}
	slices.SortStableFunc(pending, func(a, b string) int { return priority(a) - priority(b) })
	var deferred error
	for _, name := range pending {
		d := graph[name]
		if err := p.collision(name, d); err != nil {
			if deferred == nil {
				deferred = err
			}
			continue
		}
		candidates, err := p.candidates(name, d)
		if err != nil {
			var access *AccessError
			if errors.As(err, &access) {
				return finish(err)
			}
			// Expand other parents first so diagnostics include their ranges too.
			if deferred == nil {
				deferred = err
			}
			continue
		}
		var firstError error
		for _, r := range candidates {
			n, err := p.load(Node{Name: name, Source: keys(d.sources)[0], Version: r.Version, Commit: r.Commit})
			if err == nil {
				branch := maps.Clone(selected)
				predicates := constraints(d.requests)
				slices.Sort(predicates)
				branch[name] = assignment{Node: n, Constraints: slices.Compact(predicates)}
				var result map[string]assignment
				result, err = p.search(branch)
				if err == nil {
					return result, nil
				}
			}
			if firstError == nil {
				firstError = err
			}
			var access *AccessError
			var validation *ValidationError
			var unknown *UnknownSourceError
			if errors.As(err, &access) || errors.As(err, &validation) || errors.As(err, &unknown) {
				return finish(err)
			}
		}
		return finish(firstError)
	}
	for _, name := range keys(graph) {
		d := graph[name]
		if err := p.collision(name, d); err != nil {
			return finish(err)
		}
		n, assigned := selected[name]
		if !assigned {
			continue
		}
		if !matches(n.Version, d.requests) {
			if n.Version == "" {
				return finish(&NoReleasesError{Scope: p.scope, Source: n.Source, Name: name, Requests: sortedRequests(d.requests)})
			}
			t := p.targets[name]
			var blocking []Requirement
			for _, r := range d.requests {
				if !matches(n.Version, []Requirement{r}) {
					blocking = append(blocking, r)
				}
			}
			from := "uninstalled"
			if s := p.current.Skills[name]; s != nil && s.Resolved != nil {
				from = s.Resolved.Version
				if from == "" {
					from = s.Resolved.Commit
				}
			}
			return finish(&UpgradeRefusalError{Source: n.Source, Name: name, From: from, To: n.Version, Operation: t.Policy, Blocking: sortedRequests(blocking)})
		}
	}
	if deferred != nil {
		return finish(deferred)
	}
	if err := cycle(selected); err != nil {
		return finish(err)
	}
	return selected, nil
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
		if _, ok := p.targets[name]; ok {
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
		if _, ok := p.targets[name]; ok {
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
