package dependency_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
)

func TestPlannerErrorContract(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		m := input()
		m.Skills["security"] = pin("team", "3.0.0", "security", true, "code-review@^2.0.0")
		m.Skills["review"] = pin("team", "2.0.0", "review", true, "code-review@^1.0.0")
		err := failure(t, m, nil, nil)
		want := "incompatible requirements for team/code-review\n" +
			"  team/review@2.0.0 requires ^1.0.0\n" +
			"  team/security@3.0.0 requires ^2.0.0\n" +
			"  one version per skill name is allowed in project scope"
		if err.Error() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", err, want)
		}
	})
	t.Run("no_releases", func(t *testing.T) {
		m := input()
		m.Skills["review"] = pin("team", "2.0.0", "review", true, "code-review@^1.0.0")
		err := failure(t, m, nil, fake())
		want := "team/review@2.0.0 requires team/code-review@^1.0.0\n" +
			"  code-review has no published releases; a branch-tip pin cannot satisfy this range"
		if err.Error() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", err, want)
		}
	})
	t.Run("upgrade_refusal", func(t *testing.T) {
		m := input()
		m.Skills["review"] = pin("team", "2.0.0", "review", true, "code-review@^1.0.0")
		m.Skills["code-review"] = pin("team", "1.4.0", "code-review", true)
		f := fake()
		f.add("team/code-review", "2.0.0")
		err := failure(t, m, []dependency.Target{{Name: "code-review", Policy: dependency.Upgrade}}, f)
		want := "cannot upgrade team/code-review from 1.4.0 to 2.0.0\n" +
			"  team/review@2.0.0 requires ^1.0.0"
		if err.Error() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", err, want)
		}
	})
}

func TestPlannerUnknownAliasScopeRecovery(t *testing.T) {
	for _, global := range []bool{false, true} {
		m := input()
		m.Skills["review"] = pin("team", "2.0.0", "review", true, "other-source/us-review@^1.0.0")
		scope := dependency.Scope{Global: global, ManifestPath: "/selected/kitt.toml"}
		_, err := dependency.BuildPlan(scope, m, nil, nil)
		var unknown *dependency.UnknownSourceError
		if !errors.As(err, &unknown) || unknown.Requirement.Index != 0 {
			t.Fatal(err)
		}
		command := "kitt init --source"
		if global {
			command = "kitt install -g --source"
		}
		want := "unknown source alias \"other-source\"\n" +
			"  required by team/review@2.0.0: \"other-source/us-review@^1.0.0\"\n" +
			"  missing entry: [sources].other-source in /selected/kitt.toml\n" +
			"  add it with: " + command + " \"other-source=<repository-url>\""
		if err.Error() != want {
			t.Fatalf("got:\n%s\nwant:\n%s", err, want)
		}
	}
}

func TestPlannerErrorDeterminism(t *testing.T) {
	var want string
	for i := 0; i < 20; i++ {
		m := input()
		entries := []string{"z", "a", "m"}
		if i%2 == 0 {
			slices.Reverse(entries)
		}
		for _, name := range entries {
			constraint := "^1"
			if name == "z" {
				constraint = "^2"
			}
			m.Skills[name] = pin("team", "1.0.0", name, true, "d@"+constraint)
		}
		err := failure(t, m, nil, nil)
		if i == 0 {
			want = err.Error()
		} else if err.Error() != want {
			t.Fatalf("nondeterministic error:\n%s\n%s", err, want)
		}
	}
}
