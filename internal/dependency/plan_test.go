package dependency_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
)

type fakeSources struct {
	releases map[string][]dependency.Release
	requires map[string][]string
	calls    map[string]int
	err      error
}

func fake() *fakeSources {
	return &fakeSources{releases: map[string][]dependency.Release{}, requires: map[string][]string{}, calls: map[string]int{}}
}

func (f *fakeSources) add(ref, version string, requires ...string) string {
	commit := ref + "-" + version
	f.releases[ref] = append(f.releases[ref], dependency.Release{Version: version, Commit: commit})
	f.requires[ref+"@"+commit] = slices.Clone(requires)
	return commit
}

func (f *fakeSources) Releases(alias, name string) ([]dependency.Release, error) {
	f.calls["releases "+alias+"/"+name]++
	return f.releases[alias+"/"+name], f.err
}

func (f *fakeSources) Requires(alias, name, commit string) ([]string, error) {
	key := alias + "/" + name + "@" + commit
	f.calls["requires "+key]++
	if f.err != nil {
		return nil, f.err
	}
	if raw, ok := f.requires[key]; ok {
		return raw, nil
	}
	return nil, fmt.Errorf("unexpected metadata read %q", key)
}

func input() *manifest.Manifest {
	// Distinct aliases deliberately share the URL; identity is still the alias.
	return &manifest.Manifest{Sources: map[string]string{"team": "same-url", "public": "same-url"}, Skills: map[string]*manifest.Skill{}}
}

func pin(alias, version, commit string, direct bool, requires ...string) *manifest.Skill {
	raw := append([]string{}, requires...)
	return &manifest.Skill{Source: alias, Direct: &direct, Resolved: &manifest.Resolved{Version: version, Commit: commit, Requires: &raw}}
}

func target(name string) dependency.Target {
	return dependency.Target{Name: name, Source: "team", Policy: dependency.Install}
}

func build(t *testing.T, m *manifest.Manifest, targets []dependency.Target, f dependency.SourceAccess) *dependency.Plan {
	t.Helper()
	before, _ := json.Marshal(m)
	p, err := dependency.BuildPlan(dependency.Scope{}, m, targets, f)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatalf("planner mutated its input:\n%s\n%s", before, after)
	}
	return p
}

func node(t *testing.T, p *dependency.Plan, name string) dependency.Node {
	t.Helper()
	for _, n := range p.Nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("missing %q in %+v", name, p.Nodes)
	return dependency.Node{}
}

func failure(t *testing.T, m *manifest.Manifest, targets []dependency.Target, f dependency.SourceAccess) error {
	t.Helper()
	before, _ := json.Marshal(m)
	p, err := dependency.BuildPlan(dependency.Scope{}, m, targets, f)
	if p != nil || err == nil {
		t.Fatalf("BuildPlan = %+v, %v; want nil plan and error", p, err)
	}
	var planning *dependency.PlanError
	var access *dependency.AccessError
	isAccess := errors.As(err, &access)
	if (isAccess && errors.As(err, &planning)) || (!isAccess && (!errors.As(err, &planning) || !planning.ManagedStateUntouched)) || strings.Contains(err.Error(), "no changes applied") {
		t.Fatalf("missing untouched-state contract: %v", err)
	}
	after, _ := json.Marshal(m)
	if string(before) != string(after) {
		t.Fatal("failure mutated manifest")
	}
	return err
}

func TestPlannerChainAndImmediateSourceInheritance(t *testing.T) {
	f := fake()
	f.add("team/a", "1.0.0", "public/b@^2")
	f.add("public/b", "2.0.0", "c@^0.2")
	f.add("public/c", "0.2.0")
	f.add("public/c", "0.9.0")
	f.add("public/c", "1.0.0")
	p := build(t, input(), []dependency.Target{target("a")}, f)
	if len(p.Nodes) != 3 || len(p.Diff.Added) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	a, b, c := node(t, p, "a"), node(t, p, "b"), node(t, p, "c")
	if !a.Direct || b.Direct || c.Direct || c.Source != "public" || c.Version != "0.9.0" || c.Constraint != "" {
		t.Fatalf("nodes = %+v", p.Nodes)
	}
	if !slices.Equal(b.Requires, []string{"public/c@^0.2"}) || !slices.Equal(c.Via, []string{"b"}) {
		t.Fatalf("source inheritance/via = %+v", p.Nodes)
	}
}

func TestPlannerDiamondAndDeduplication(t *testing.T) {
	f := fake()
	f.add("team/a", "1.0.0", "c@^1", "b@^1", "team/b@^1")
	f.add("team/b", "1.0.0", "d@^1", "d@^1", "d@^1.2")
	f.add("team/c", "1.0.0", "d@1.5.0")
	f.add("team/d", "1.2.0")
	f.add("team/d", "1.5.0")
	p := build(t, input(), []dependency.Target{target("a")}, f)
	d := node(t, p, "d")
	if len(p.Nodes) != 4 || len(node(t, p, "a").Requires) != 2 || len(node(t, p, "b").Requires) != 2 || d.Version != "1.5.0" || !slices.Equal(d.Via, []string{"b", "c"}) {
		t.Fatalf("diamond = %+v", p.Nodes)
	}
	for call, count := range f.calls {
		if count != 1 {
			t.Fatalf("unmemoized source call %q: %d", call, count)
		}
	}
}

func TestPlannerConflictIncludesAllRequestersAndManifestConstraint(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "d@^1", "d@^1.2")
	m.Skills["b"] = pin("team", "2.0.0", "b", true, "d@^2")
	m.Skills["d"] = pin("team", "1.5.0", "d", false)
	m.Skills["d"].Version = "1.5.0"
	err := failure(t, m, nil, fake())
	var conflict *dependency.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != dependency.EmptyIntersection || len(conflict.Requests) != 4 {
		t.Fatalf("conflict = %#v, %v", conflict, err)
	}
	if conflict.Requests[0].Index != 0 || conflict.Requests[1].Index != 1 || !conflict.Requests[3].Manifest {
		t.Fatalf("provenance = %+v", conflict.Requests)
	}
	if !strings.Contains(err.Error(), "metadata.requires[0]") || !strings.Contains(err.Error(), "metadata.requires[1]") {
		t.Fatalf("duplicate declarations lack their indexes: %v", err)
	}
}

func TestPlannerSourceCollisionEvenWithIdenticalURLs(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(fmt.Sprint(current), func(t *testing.T) {
			m := input()
			m.Skills["a"] = pin("team", "1.0.0", "a", true, "d@^1")
			m.Skills["b"] = pin("team", "1.0.0", "b", true, "public/d@^1")
			if current {
				m.Skills["d"] = pin("team", "1.0.0", "d", false)
			}
			err := failure(t, m, nil, nil)
			var conflict *dependency.ConflictError
			if !errors.As(err, &conflict) || conflict.Reason != dependency.SourceCollision || !slices.Equal(conflict.Sources, []string{"public", "team"}) || !strings.Contains(err.Error(), `"public/d", "team/d"`) {
				t.Fatalf("source conflict = %+v, %v", conflict, err)
			}
		})
	}
}

func TestPlannerRetainsCompatiblePinWithoutSourceAccess(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "2.0.0", "a", true, "team/d@^1")
	m.Skills["d"] = pin("team", "1.2.0", "original-commit", true)
	m.Skills["d"].Version = "^1"
	m.Skills["d"].Via = []string{"a"}
	p := build(t, m, nil, nil)
	if node(t, p, "d").Commit != "original-commit" || len(p.Diff.Unchanged) != 2 || len(p.Diff.Changed) != 0 {
		t.Fatalf("pin changed: %+v", p)
	}
}

func TestPlannerNeverMovesCompatiblePinToEscapeConflict(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "b@^1", "d@^2")
	m.Skills["b"] = pin("team", "1.0.0", "b", false, "d@^1")
	f := fake()
	f.add("team/b", "1.9.0", "d@^2") // Would resolve the conflict, but b's pin is compatible.
	f.add("team/d", "1.0.0")
	f.add("team/d", "2.0.0")
	err := failure(t, m, nil, f)
	var conflict *dependency.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != dependency.EmptyIntersection || f.calls["releases team/b"] != 0 {
		t.Fatalf("compatible pin was moved: %v; calls=%v", err, f.calls)
	}
}

func TestPlannerRangeFailureKinds(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
		versions            []string
		reason              dependency.ConflictReason
		noReleases          bool
	}{
		{name: "disjoint_major", first: "^1", second: "^2", reason: dependency.EmptyIntersection},
		{name: "overlap_without_release", first: "^1.2", second: "^1.5", versions: []string{"1.4.0", "2.0.0"}, reason: dependency.NoMatchingRelease},
		{name: "exact_excludes_range", first: "1.0.0", second: "^1.2", reason: dependency.EmptyIntersection},
		{name: "unequal_exact", first: "1.0.0", second: "1.0.1", reason: dependency.EmptyIntersection},
		{name: "unpublished_exact", first: "1.5.0", second: "^1", versions: []string{"1.4.0"}, reason: dependency.NoMatchingRelease},
		{name: "no_releases", first: "^1", second: "^1.2", noReleases: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := input()
			m.Skills["a"] = pin("team", "1.0.0", "a", true, "d@"+tc.first)
			m.Skills["b"] = pin("team", "1.0.0", "b", true, "d@"+tc.second)
			f := fake()
			for _, v := range tc.versions {
				f.add("team/d", v)
			}
			err := failure(t, m, nil, f)
			if tc.noReleases {
				var no *dependency.NoReleasesError
				if !errors.As(err, &no) || len(no.Requests) != 2 || !strings.Contains(err.Error(), "a branch-tip pin cannot satisfy this range") {
					t.Fatal(err)
				}
			} else {
				var conflict *dependency.ConflictError
				if !errors.As(err, &conflict) || conflict.Reason != tc.reason || len(conflict.Requests) != 2 {
					t.Fatalf("conflict = %+v, %v", conflict, err)
				}
			}
		})
	}
}

func TestPlannerCycles(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    *manifest.Manifest
		path []string
	}{
		{name: "self", m: func() *manifest.Manifest {
			m := input()
			m.Skills["a"] = pin("team", "1.0.0", "a", true, "a@^1")
			return m
		}(), path: []string{"team/a@1.0.0", "team/a@1.0.0"}},
		{name: "multiple_sources", m: func() *manifest.Manifest {
			m := input()
			m.Skills["a"] = pin("team", "1.0.0", "a", true, "public/b@^2")
			m.Skills["b"] = pin("public", "2.0.0", "b", false, "c@^1")
			m.Skills["c"] = pin("public", "1.0.0", "c", false, "team/a@^1")
			return m
		}(), path: []string{"team/a@1.0.0", "public/b@2.0.0", "public/c@1.0.0", "team/a@1.0.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := failure(t, tc.m, nil, nil)
			var cycle *dependency.CycleError
			if !errors.As(err, &cycle) || !slices.Equal(cycle.Path, tc.path) || err.Error() != "dependency cycle: "+strings.Join(tc.path, " -> ") {
				t.Fatalf("cycle = %+v, %v", cycle, err)
			}
		})
	}
}

func TestPlannerBacktracksAndRemovesAbandonedEdges(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1", "z@^1")
	f.add("team/a", "1.0.0", "c@^1")
	f.add("team/a", "1.1.0", "b@^1", "stale@^2")
	f.add("team/b", "1.0.0", "z@^2")
	f.add("team/c", "1.0.0", "stale@^1")
	f.add("team/z", "1.0.0")
	f.add("team/stale", "1.0.0")
	f.add("team/stale", "2.0.0")
	p := build(t, input(), []dependency.Target{target("root")}, f)
	if node(t, p, "a").Version != "1.0.0" || node(t, p, "stale").Version != "1.0.0" || !slices.Equal(node(t, p, "stale").Via, []string{"c"}) {
		t.Fatalf("backtracking failed: %+v", p)
	}
	for _, n := range p.Nodes {
		if n.Name == "b" {
			t.Fatal("abandoned candidate's descendant retained")
		}
	}
	for call, count := range f.calls {
		if count > 1 {
			t.Fatalf("source reads not memoized: %s = %d", call, count)
		}
	}
}

func TestPlannerLateConstraintReplacesEarlierSelection(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1", "z@^1")
	f.add("team/a", "1.0.0")
	f.add("team/a", "1.9.0", "stale@^1")
	f.add("team/z", "1.0.0", "a@1.0.0")
	f.add("team/stale", "1.0.0")
	p := build(t, input(), []dependency.Target{target("root")}, f)
	if node(t, p, "a").Version != "1.0.0" || len(p.Nodes) != 3 {
		t.Fatalf("late constraint: %+v", p.Nodes)
	}
}

func TestPlannerBacktracksWhenCandidateIntroducesAnUnsatisfiableCycle(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1")
	f.add("team/a", "1.0.0")
	f.add("team/a", "1.9.0", "b@^1")
	f.add("team/b", "1.0.0", "a@1.0.0")
	p := build(t, input(), []dependency.Target{target("root")}, f)
	if node(t, p, "a").Version != "1.0.0" || len(p.Nodes) != 2 {
		t.Fatalf("candidate cycle prevented backtracking: %+v", p)
	}
}

func TestPlannerRestoresPinAfterProvisionalConstraintDisappears(t *testing.T) {
	m := input()
	m.Skills["b"] = pin("team", "1.0.0", "original-b", false)
	f := fake()
	f.add("team/root", "1.0.0", "a@^1", "z@^1")
	f.add("team/a", "1.9.0", "b@^1.5")
	f.add("team/a", "1.0.0", "b@^1")
	f.add("team/b", "1.9.0", "stale@^1")
	f.add("team/z", "1.0.0", "a@1.0.0")
	f.add("team/stale", "1.0.0")
	p := build(t, m, []dependency.Target{target("root")}, f)
	if b := node(t, p, "b"); b.Commit != "original-b" || len(b.Requires) != 0 || len(p.Nodes) != 4 {
		t.Fatalf("compatible original pin not restored: %+v", p)
	}
}

func TestPlannerReconsidersHighestReleaseWhenAConstraintDisappears(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1", "b@^1", "z@^1")
	f.add("team/a", "1.9.0", "b@1.0.0")
	f.add("team/a", "1.0.0", "b@^1")
	f.add("team/b", "1.9.0")
	f.add("team/b", "1.0.0")
	f.add("team/z", "1.0.0", "a@1.0.0")
	p := build(t, input(), []dependency.Target{target("root")}, f)
	if node(t, p, "b").Version != "1.9.0" {
		t.Fatalf("abandoned exact constraint still influenced selection: %+v", p)
	}
}

func TestPlannerUpgradeCannotSelectLowerThanCurrentVersion(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "2.0.0", "a", true)
	f := fake()
	f.add("team/a", "1.9.0")
	err := failure(t, m, []dependency.Target{{Name: "a", Policy: dependency.Upgrade}}, f)
	var refusal *dependency.UpgradeRefusalError
	if !errors.As(err, &refusal) || refusal.From != "2.0.0" || refusal.To != "1.9.0" {
		t.Fatal(err)
	}
}

func TestPlannerIncompatibleExistingPinCanMove(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "d@^2")
	m.Skills["d"] = pin("team", "1.0.0", "d", false, "old@^1")
	m.Skills["old"] = pin("team", "1.0.0", "old", false)
	f := fake()
	f.add("team/d", "2.0.0")
	p := build(t, m, nil, f)
	if node(t, p, "d").Version != "2.0.0" || !slices.Equal(p.Orphans, []string{"old"}) || len(node(t, p, "old").Via) != 0 {
		t.Fatalf("replacement/orphan = %+v", p)
	}
	if f.calls["requires team/d@d"] != 0 {
		t.Fatal("read metadata for incompatible abandoned pin")
	}
}

func TestPlannerExplicitTargetNeverDowngradedToAvoidConflict(t *testing.T) {
	for _, policy := range []dependency.CandidatePolicy{dependency.Install, dependency.Upgrade} {
		t.Run(string(policy), func(t *testing.T) {
			m := input()
			m.Skills["d"] = pin("team", "1.4.0", "d", true)
			m.Skills["review"] = pin("team", "2.0.0", "review", true, "d@^1")
			m.Skills["security"] = pin("team", "3.0.0", "security", true, "d@1.4.0")
			f := fake()
			f.add("team/d", "1.4.0")
			f.add("team/d", "2.0.0")
			target := target("d")
			target.Policy = policy
			if policy == dependency.Install {
				target.Constraint = "^2"
			}
			err := failure(t, m, []dependency.Target{target}, f)
			var refusal *dependency.UpgradeRefusalError
			if !errors.As(err, &refusal) || refusal.To != "2.0.0" || len(refusal.Blocking) != 2 {
				t.Fatalf("target was downgraded: %+v, %v", refusal, err)
			}
		})
	}
}

func TestPlannerExplicitDependentDoesNotBacktrack(t *testing.T) {
	m := input()
	m.Skills["guard"] = pin("team", "1.0.0", "guard", true, "d@^1")
	f := fake()
	f.add("team/a", "1.0.0", "d@^1")
	f.add("team/a", "2.0.0", "d@^2")
	f.add("team/d", "1.0.0")
	f.add("team/d", "2.0.0")
	err := failure(t, m, []dependency.Target{target("a")}, f)
	var conflict *dependency.ConflictError
	if !errors.As(err, &conflict) || conflict.Name != "d" || conflict.Requests[0].Version != "2.0.0" {
		t.Fatalf("explicit dependent silently downgraded: %v", err)
	}
}

func TestPlannerUpgradeRefusalRetainsBlockersWhenCandidateClosureAlsoFails(t *testing.T) {
	m := input()
	m.Skills["review"] = pin("team", "1.0.0", "review", true, "d@^1")
	m.Skills["d"] = pin("team", "1.0.0", "d", false)
	f := fake()
	f.add("team/d", "2.0.0", "unpublished@^1")
	err := failure(t, m, []dependency.Target{{Name: "d", Policy: dependency.Upgrade}}, f)
	var refusal *dependency.UpgradeRefusalError
	if !errors.As(err, &refusal) || len(refusal.Blocking) != 1 || refusal.Blocking[0].Requester != "review" {
		t.Fatalf("blocking dependent hidden by candidate closure error: %v", err)
	}
}

func TestPlannerOrphanRequirementsStillConstrainScope(t *testing.T) {
	m := input()
	m.Skills["root"] = pin("team", "1.0.0", "root", true, "d@^1")
	m.Skills["orphan"] = pin("team", "1.0.0", "orphan", false, "d@^2")
	err := failure(t, m, nil, nil)
	var conflict *dependency.ConflictError
	if !errors.As(err, &conflict) || len(conflict.Requests) != 2 || conflict.Requests[0].Requester != "orphan" {
		t.Fatalf("orphan constraint discarded: %v", err)
	}
}

func TestPlannerUpgradeTargetsTogetherAndRetainsOrphans(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "d@^1", "orphan@^1")
	m.Skills["d"] = pin("team", "1.0.0", "d", false)
	m.Skills["orphan"] = pin("team", "1.0.0", "orphan", false, "child@^1")
	m.Skills["child"] = pin("team", "1.0.0", "child", false)
	f := fake()
	f.add("team/a", "2.0.0", "d@^2")
	f.add("team/d", "2.0.0")
	targets := []dependency.Target{{Name: "d", Policy: dependency.Upgrade}, {Name: "a", Policy: dependency.Upgrade}}
	p := build(t, m, targets, f)
	if node(t, p, "a").Version != "2.0.0" || node(t, p, "d").Version != "2.0.0" || !node(t, p, "d").Direct || !slices.Equal(p.Orphans, []string{"child", "orphan"}) {
		t.Fatalf("joint upgrade = %+v", p)
	}
	if !slices.Equal(node(t, p, "child").Via, []string{"orphan"}) {
		t.Fatal("orphan subtree discarded")
	}
}

func TestPlannerPromotesExistingTransitivePin(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "team/d@^1")
	m.Skills["d"] = pin("team", "1.0.0", "d", false)
	m.Skills["d"].Via = []string{"a"}
	p := build(t, m, []dependency.Target{{Name: "d"}}, nil)
	if !node(t, p, "d").Direct || node(t, p, "d").Commit != "d" || !slices.Equal(node(t, p, "d").Via, []string{"a"}) || len(p.Diff.Changed) != 1 {
		t.Fatalf("promotion = %+v", p)
	}
}

func TestPlannerLegacyPinReadsItsCommitOnce(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "old-commit", true)
	m.Skills["a"].Resolved.Requires = nil
	f := fake()
	f.requires["team/a@old-commit"] = []string{"b@^1"}
	f.add("team/b", "1.0.0")
	p := build(t, m, nil, f)
	if f.calls["requires team/a@old-commit"] != 1 || f.calls["releases team/a"] != 0 || node(t, p, "a").Commit != "old-commit" || !slices.Equal(node(t, p, "a").Requires, []string{"team/b@^1"}) {
		t.Fatalf("legacy metadata = %+v; calls=%v", p, f.calls)
	}
}

func TestPlannerBranchTipCannotSatisfyRange(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "", "branch-a", true, "d@^1")
	m.Skills["d"] = pin("team", "", "branch-d", false)
	f := fake()
	err := failure(t, m, nil, f)
	var no *dependency.NoReleasesError
	if !errors.As(err, &no) || no.Name != "d" {
		t.Fatal(err)
	}
	delete(m.Skills, "a")
	p := build(t, m, nil, nil)
	if node(t, p, "d").Commit != "branch-d" {
		t.Fatal("unconstrained existing branch-tip pin changed")
	}
}

func TestPlannerDeterminismAcrossMapAndInputOrdering(t *testing.T) {
	var want string
	for i := 0; i < 30; i++ {
		m := input()
		entries := map[string]*manifest.Skill{
			"x": pin("team", "1.0.0", "x", true, "team/d@^1"),
			"y": pin("team", "1.0.0", "y", true, "team/d@^1.2"),
		}
		for name, entry := range entries {
			m.Skills[name] = entry
		}
		f := fake()
		f.add("team/d", "1.2.0")
		f.add("team/x", "1.0.0", "team/d@^1")
		f.add("team/y", "1.0.0", "team/d@^1.2")
		f.add("team/d", "1.9.0")
		if i%2 == 0 {
			slices.Reverse(f.releases["team/d"])
		}
		targets := []dependency.Target{{Name: "x"}, {Name: "y"}}
		if i%2 == 0 {
			slices.Reverse(targets)
		}
		p := build(t, m, targets, f)
		data, _ := json.Marshal(p)
		if i == 0 {
			want = string(data)
		} else if string(data) != want {
			t.Fatalf("nondeterministic plan:\n%s\n%s", data, want)
		}
	}
}

func TestPlannerValidationBeforeSourceAccess(t *testing.T) {
	for _, mutate := range []func(*manifest.Manifest){
		func(m *manifest.Manifest) { m.Skills["bad_name"] = &manifest.Skill{Source: "team"} },
		func(m *manifest.Manifest) { m.Skills["z"] = &manifest.Skill{Source: "team", Version: "~1"} },
		func(m *manifest.Manifest) { m.Skills["z"] = pin("team", "1.0.0", "z", true, "bad@") },
		func(m *manifest.Manifest) { m.Skills["z"] = nil },
	} {
		m := input()
		m.Skills["a"] = &manifest.Skill{Source: "team"}
		mutate(m)
		f := fake()
		err := failure(t, m, nil, f)
		var validation *dependency.ValidationError
		if !errors.As(err, &validation) || len(f.calls) != 0 {
			t.Fatalf("validation accessed source: %v, %v", err, f.calls)
		}
	}
}

func TestPlannerAccessFailurePreservesCause(t *testing.T) {
	f := fake()
	f.err = errors.New("git unavailable")
	err := failure(t, input(), []dependency.Target{target("a")}, f)
	var access *dependency.AccessError
	if !errors.Is(err, f.err) || !errors.As(err, &access) || access.Operation != "list releases" {
		t.Fatal(err)
	}
}

func TestPlannerDoesNotMutateSourceSlices(t *testing.T) {
	f := fake()
	f.add("team/a", "1.0.0", "d@^1", "d@^1")
	f.add("team/d", "1.1.0")
	f.add("team/d", "1.0.0")
	versions := slices.Clone(f.releases["team/d"])
	requires := maps.Clone(f.requires)
	build(t, input(), []dependency.Target{target("a")}, f)
	if !slices.Equal(f.releases["team/d"], versions) || !reflect.DeepEqual(f.requires, requires) {
		t.Fatal("source data mutated")
	}
}

// Exhaustively enumerate complete assignments independently of the planner, then
// compare feasibility, every edge, pin retention and deterministic preference.
func TestPlannerSearchMatchesExhaustiveGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		f := fake()
		f.add("team/root", "1.0.0", "a@^1")
		m := input()
		definitions := map[string]map[string][]string{}
		names := []string{"a", "b", "c", "d", "e"}
		versions := []string{"1.0.0", "1.1.0"}
		pins := map[string]string{}
		// Fixed leaf pins may be direct or orphaned. Random incoming constraints
		// sometimes reject them; they must never be moved to rescue a new choice.
		if trial%3 != 0 {
			pins["e"] = versions[rng.Intn(2)]
		}
		if trial%5 == 0 {
			pins["d"] = versions[rng.Intn(2)]
		}
		for _, name := range names {
			definitions[name] = map[string][]string{}
			for _, version := range versions {
				var requires []string
				if pins[name] == "" {
					for count := rng.Intn(3); count > 0; count-- {
						requires = append(requires, names[rng.Intn(len(names))]+"@"+[]string{"^1", "1.0.0", "1.1.0"}[rng.Intn(3)])
					}
				}
				definitions[name][version] = requires
				commit := f.add("team/"+name, version, requires...)
				if pins[name] == version {
					m.Skills[name] = pin("team", version, commit, trial%2 == 0)
				}
			}
		}
		var feasible []map[string]string
		for choice := 0; choice < 1<<len(names); choice++ {
			selected := map[string]string{}
			pinned := true
			for i, name := range names {
				selected[name] = versions[(choice>>i)&1]
				if pins[name] != "" && selected[name] != pins[name] {
					pinned = false
				}
			}
			if !pinned {
				continue
			}
			active, done := map[string]bool{}, map[string]bool{}
			var valid func(string) bool
			valid = func(name string) bool {
				if active[name] {
					return false
				}
				if done[name] {
					return true
				}
				active[name] = true
				for _, text := range definitions[name][selected[name]] {
					r, err := dependency.ParseRef(text)
					if err != nil {
						t.Fatal(err)
					}
					match, _ := source.Satisfies(r.Constraint, selected[r.Name])
					if !match || !valid(r.Name) {
						return false
					}
				}
				active[name], done[name] = false, true
				return true
			}
			ok := valid("a")
			for name := range pins {
				ok = ok && valid(name)
			}
			if ok {
				reachable := map[string]string{}
				for name := range done {
					reachable[name] = selected[name]
				}
				feasible = append(feasible, reachable)
			}
		}
		// Compare feasible assignments by the highest release at the first
		// differing decision in the common deterministic frontier. This ordering
		// does not depend on enumeration order or the planner's pruning algorithm.
		preferred := func(a, b map[string]string) bool {
			pending := map[string]bool{"a": true}
			visited := map[string]bool{}
			for name := range pins {
				pending[name] = true
			}
			for len(pending) > 0 {
				ordered := slices.Sorted(maps.Keys(pending))
				slices.SortStableFunc(ordered, func(a, b string) int {
					pa, pb := 0, 0
					if pins[a] != "" {
						pa = -1
					}
					if pins[b] != "" {
						pb = -1
					}
					return pa - pb
				})
				name := ordered[0]
				delete(pending, name)
				if visited[name] {
					continue
				}
				visited[name] = true
				if a[name] != b[name] {
					return a[name] > b[name]
				}
				for _, text := range definitions[name][a[name]] {
					r, _ := dependency.ParseRef(text)
					if !visited[r.Name] {
						pending[r.Name] = true
					}
				}
			}
			return false
		}
		var best map[string]string
		for _, candidate := range feasible {
			if best == nil || preferred(candidate, best) {
				best = candidate
			}
		}
		p, err := dependency.BuildPlan(dependency.Scope{}, m, []dependency.Target{target("root")}, f)
		if (err == nil) != (best != nil) {
			t.Fatalf("trial %d: best=%v, plan=%+v, err=%v, pins=%v, definitions=%v", trial, best, p, err, pins, definitions)
		}
		if err != nil {
			var conflict *dependency.ConflictError
			var cycle *dependency.CycleError
			if !errors.As(err, &conflict) && !errors.As(err, &cycle) {
				t.Fatalf("unstructured trial %d: %v", trial, err)
			}
			continue
		}
		actual := map[string]dependency.Node{}
		for _, n := range p.Nodes {
			actual[n.Name] = n
		}
		if len(actual) != len(best)+1 {
			t.Fatalf("trial %d: stale or missing nodes: %+v, best=%v", trial, p.Nodes, best)
		}
		for name, version := range best {
			n := actual[name]
			if n.Version != version {
				t.Fatalf("trial %d: %s=%s; highest feasible=%s; pins=%v, definitions=%v", trial, name, n.Version, version, pins, definitions)
			}
			if pins[name] != "" && n.Commit != m.Skills[name].Resolved.Commit {
				t.Fatalf("trial %d: pin moved: %+v", trial, n)
			}
		}
		for _, n := range p.Nodes {
			for _, text := range n.Requires {
				r, _ := dependency.ParseRef(text)
				dep, ok := actual[r.Name]
				match, _ := source.Satisfies(r.Constraint, dep.Version)
				if !ok || !match || dep.Source != r.Source {
					t.Fatalf("trial %d: broken edge %s -> %s: %+v", trial, n.Name, text, p.Nodes)
				}
			}
		}
	}
}
