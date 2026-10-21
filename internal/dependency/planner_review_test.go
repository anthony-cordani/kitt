package dependency_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
)

func explored(t *testing.T, err error, limit int) {
	t.Helper()
	var plan *dependency.PlanError
	if !errors.As(err, &plan) || plan.ExploredStates > limit {
		t.Fatalf("expected at most %d explored states: %#v, %v", limit, plan, err)
	}
}

func TestPlannerUnsatisfiableSearchBounds(t *testing.T) {
	for _, kind := range []string{"source_collision", "no_releases", "mutual_cycle", "invalid_publications", "unknown_alias", "invalid_requirement"} {
		t.Run(kind, func(t *testing.T) {
			f := fake()
			root := []string{"a@^1", "b@^1", "c@^1", "d@^1", "e@^1", "f@^1"}
			if kind == "no_releases" {
				root = append(root, "missing@^1")
			}
			f.add("team/root", "1.0.0", root...)
			for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
				for i := 0; i < 10; i++ {
					var requires []string
					switch kind {
					case "source_collision":
						if name == "a" {
							requires = []string{"team/x@^1"}
						}
						if name == "f" {
							requires = []string{"public/x@^1"}
						}
					case "mutual_cycle":
						if name == "a" {
							requires = []string{"f@^1"}
						}
						if name == "f" {
							requires = []string{"a@^1"}
						}
					case "unknown_alias":
						if name == "f" {
							requires = []string{"unknown/x@^1"}
						}
					case "invalid_requirement":
						if name == "f" {
							requires = []string{"../x@^1"}
						}
					}
					f.add("team/"+name, fmt.Sprintf("1.%d.0", i), requires...)
				}
			}
			f.add("team/x", "1.0.0")
			f.add("public/x", "1.0.0")
			if kind == "invalid_publications" {
				f.releases["team/f"] = []dependency.Release{{Version: "bogus", Commit: "f"}}
			}
			err := failure(t, input(), []dependency.Target{target("root")}, f)
			explored(t, err, 200)
			switch kind {
			case "source_collision":
				var e *dependency.ConflictError
				if !errors.As(err, &e) || e.Reason != dependency.SourceCollision {
					t.Fatal(err)
				}
			case "mutual_cycle":
				var e *dependency.CycleError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
			case "no_releases":
				var e *dependency.NoReleasesError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				explored(t, err, 2)
			case "invalid_publications", "invalid_requirement":
				var e *dependency.ValidationError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				explored(t, err, 8)
			case "unknown_alias":
				var e *dependency.UnknownSourceError
				if !errors.As(err, &e) {
					t.Fatal(err)
				}
				explored(t, err, 8)
			}
		})
	}
}

func TestPlannerNewDependencyPreservesPins(t *testing.T) {
	for _, older := range []bool{true, false} {
		t.Run(fmt.Sprint(older), func(t *testing.T) {
			m := input()
			m.Skills["x"] = pin("team", "1.0.0", "original-x", true)
			f := fake()
			f.add("team/root", "1.0.0", "y@^1")
			// Both releases fit ^1 while requiring different majors of x.
			f.add("team/y", "1.9.0", "x@^2")
			if older {
				f.add("team/y", "1.0.0", "x@^1")
			}
			f.add("team/x", "2.0.0")
			if older {
				p := build(t, m, []dependency.Target{target("root")}, f)
				if node(t, p, "y").Version != "1.0.0" || node(t, p, "x").Commit != "original-x" {
					t.Fatalf("moved compatible pin: %+v", p)
				}
			} else {
				err := failure(t, m, []dependency.Target{target("root")}, f)
				var e *dependency.ConflictError
				if !errors.As(err, &e) || e.Name != "x" || !strings.Contains(err.Error(), "team/x is pinned at 1.0.0; an explicit install or upgrade is required; use kitt upgrade x") {
					t.Fatal(err)
				}
			}
			if f.calls["releases team/x"] != 0 {
				t.Fatal("consulted replacements for fixed pin")
			}
		})
	}
}

func TestPlannerInstallingNewTargetPrefersReleaseThatKeepsPin(t *testing.T) {
	m := input()
	m.Skills["x"] = pin("team", "1.0.0", "original-x", true)
	f := fake()
	f.add("team/y", "2.0.0", "x@^2")
	f.add("team/y", "1.0.0", "x@^1")
	p := build(t, m, []dependency.Target{target("y")}, f)
	if node(t, p, "y").Version != "1.0.0" || node(t, p, "x").Commit != "original-x" {
		t.Fatalf("moved compatible pin: %+v", p)
	}
}

func TestPlannerExcludingSelfDependencyIsCycle(t *testing.T) {
	for _, targeted := range []bool{true, false} {
		m := input()
		m.Skills["a"] = pin("team", "1.0.0", "a", true, "a@^2")
		var targets []dependency.Target
		if targeted {
			targets = []dependency.Target{target("a")}
		}
		err := failure(t, m, targets, nil)
		var e *dependency.CycleError
		if !errors.As(err, &e) || err.Error() != "dependency cycle: team/a@1.0.0 -> team/a@1.0.0" {
			t.Fatal(err)
		}
	}
}

func TestPlannerDowngradeExplainsPinWithoutUnrelatedBlockers(t *testing.T) {
	m := input()
	m.Skills["d"] = pin("team", "2.0.0", "d", true)
	m.Skills["d"].Version = "^1"
	m.Skills["review"] = pin("team", "1.0.0", "review", true, "d@^1")
	f := fake()
	f.add("team/d", "1.9.0")
	err := failure(t, m, []dependency.Target{{Name: "d", Policy: dependency.Upgrade}}, f)
	var e *dependency.UpgradeRefusalError
	if !errors.As(err, &e) || len(e.Blocking) != 0 || !strings.Contains(err.Error(), "only matching release is below the pin") {
		t.Fatal(err)
	}
}

func TestPlannerInstallRefusalWording(t *testing.T) {
	for _, installed := range []bool{false, true} {
		m := input()
		m.Skills["guard"] = pin("team", "1.0.0", "guard", true, "x@^1")
		if installed {
			m.Skills["x"] = pin("team", "2.0.0", "x", true)
		}
		f := fake()
		f.add("team/x", "2.0.0")
		err := failure(t, m, []dependency.Target{target("x")}, f)
		var e *dependency.UpgradeRefusalError
		if !errors.As(err, &e) || !strings.HasPrefix(err.Error(), "cannot install team/x@2.0.0\n") {
			t.Fatal(err)
		}
	}
}

func TestPlannerAmbiguousTargetNamesConfiguredAliases(t *testing.T) {
	err := failure(t, input(), []dependency.Target{{Name: "review"}}, fake())
	var e *dependency.ValidationError
	if !errors.As(err, &e) || !strings.Contains(err.Error(), "alias/ qualifier") || !strings.Contains(err.Error(), "public, team") {
		t.Fatal(err)
	}
}

func TestPlannerAbandonedDropPathsDoNotPoisonSharedState(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1", "b@^1", "z@^1")
	f.add("team/a", "1.9.0", "d@^1")
	f.add("team/a", "1.0.0")
	f.add("team/b", "1.9.0", "e@^1")
	f.add("team/b", "1.0.0")
	f.add("team/d", "1.0.0", "z@^1")
	f.add("team/e", "1.0.0", "z@^1")
	f.add("team/z", "1.0.0", "a@1.0.0", "b@1.0.0")
	// The former drop-based search could reach the common root/z state while
	// removing a/d, then again while removing b/e. A failure relative to the
	// first active stack must not poison the second path. Immutable branches
	// rebuild that shared frontier without retaining either abandoned subtree.
	p := build(t, input(), []dependency.Target{target("root")}, f)
	if node(t, p, "a").Version != "1.0.0" || node(t, p, "b").Version != "1.0.0" || len(p.Nodes) != 4 {
		t.Fatalf("abandoned paths poisoned shared state: %+v", p)
	}
}

type failingMetadata struct {
	*fakeSources
	commit string
	err    error
}

func (f failingMetadata) Requires(alias, name, commit string) ([]string, error) {
	if commit == f.commit {
		return nil, f.err
	}
	return f.fakeSources.Requires(alias, name, commit)
}

func TestPlannerLaterRealErrorCannotBeReplacedByRevisit(t *testing.T) {
	f := fake()
	f.add("team/root", "1.0.0", "a@^1")
	f.add("team/a", "1.9.0", "b@^1")
	f.add("team/b", "1.0.0", "a@1.0.0")
	commit := f.add("team/a", "1.0.0")
	real := errors.New("metadata commit unavailable")
	err := failure(t, input(), []dependency.Target{target("root")}, failingMetadata{f, commit, real})
	var access *dependency.AccessError
	if !errors.Is(err, real) || !errors.As(err, &access) || strings.Contains(err.Error(), "already examined") {
		t.Fatalf("later real error replaced: %v", err)
	}
}

func TestPlannerFixedSourceCollisionStopsBeforeUnrelatedChoices(t *testing.T) {
	m := input()
	m.Skills["a"] = pin("team", "1.0.0", "a", true, "x@^1")
	m.Skills["b"] = pin("team", "1.0.0", "b", true, "public/x@^1")
	m.Skills["c"] = &manifest.Skill{Source: "team"}
	f := fake()
	f.err = errors.New("unrelated source failure")
	err := failure(t, m, nil, f)
	var conflict *dependency.ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != dependency.SourceCollision || len(f.calls) != 0 {
		t.Fatalf("fixed collision did not stop immediately: %v, calls=%v", err, f.calls)
	}
	explored(t, err, 3)
}
