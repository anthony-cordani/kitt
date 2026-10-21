package dependency_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestGitPlannerUsesSelectedTaggedCommitAndRejectsUntaggedDependency(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	d1 := testutil.Release(t, repo, "dependency", "1.0.0")
	d2 := testutil.Release(t, repo, "dependency", "2.0.0")
	testutil.WriteSkillRequires(t, repo, "parent", "1.0.0", []string{"dependency@^1"})
	p1 := testutil.Commit(t, repo, "parent release with v1 requirement")
	testutil.Git(t, repo, "tag", "parent/v1.0.0")
	testutil.WriteSkillRequires(t, repo, "parent", "2.0.0", []string{"dependency@^2"})
	p2 := testutil.Commit(t, repo, "parent release with v2 requirement")
	testutil.Git(t, repo, "tag", "-a", "parent/v2.0.0", "-m", "annotated release")
	testutil.WriteSkillRequires(t, repo, "untagged", "99.0.0", nil)
	testutil.WriteSkillRequires(t, repo, "parent", "3.0.0", []string{"untagged@^99"})
	head := testutil.Commit(t, repo, "unreleased HEAD has different requirements")
	testutil.WriteFile(t, filepath.Join(repo, "unrelated.txt"), "not a skill")
	testutil.Commit(t, repo, "unrelated HEAD change")
	m := &manifest.Manifest{Sources: map[string]string{"team": testutil.URL(repo)}, Skills: map[string]*manifest.Skill{}}
	for _, tc := range []struct{ version, commit, dependency, depCommit string }{
		{"1.0.0", p1, "1.0.0", d1},
		{"2.0.0", p2, "2.0.0", d2},
	} {
		t.Run(tc.version, func(t *testing.T) {
			p := build(t, m, []dependency.Target{{Name: "parent", Source: "team", Constraint: tc.version}}, dependency.NewGitSources(m.Sources))
			parent, dep := node(t, p, "parent"), node(t, p, "dependency")
			if parent.Commit != tc.commit || dep.Commit != tc.depCommit || dep.Version != tc.dependency || !slices.Equal(parent.Requires, []string{"team/dependency@^" + tc.dependency[:1]}) {
				t.Fatalf("metadata was not read at selected tag: %+v", p)
			}
		})
	}
	t.Run("unreleased_parent_cannot_use_frontmatter_version_as_release", func(t *testing.T) {
		m := &manifest.Manifest{Sources: m.Sources, Skills: map[string]*manifest.Skill{"parent": pin("team", "", head, true)}}
		m.Skills["parent"].Resolved.Requires = nil
		err := failure(t, m, nil, dependency.NewGitSources(m.Sources))
		var no *dependency.NoReleasesError
		if !errors.As(err, &no) || no.Name != "untagged" {
			t.Fatalf("branch tip incorrectly satisfied a range: %v", err)
		}
	})
	t.Run("unconstrained_direct_branch_tip", func(t *testing.T) {
		p := build(t, m, []dependency.Target{{Name: "untagged", Source: "team"}}, dependency.NewGitSources(m.Sources))
		if n := node(t, p, "untagged"); n.Version != "" || n.Commit == "" || !n.Direct {
			t.Fatalf("branch tip = %+v", n)
		}
	})
	t.Run("unresolved_direct_branch_tip", func(t *testing.T) {
		m := &manifest.Manifest{Sources: m.Sources, Skills: map[string]*manifest.Skill{"untagged": {Source: "team"}}}
		p := build(t, m, nil, dependency.NewGitSources(m.Sources))
		if n := node(t, p, "untagged"); n.Version != "" || n.Commit == "" || !n.Direct {
			t.Fatalf("unresolved branch tip = %+v", n)
		}
	})
	t.Run("cache_only_tag_is_not_published", func(t *testing.T) {
		s, err := source.Open("team", m.Sources["team"])
		if err != nil {
			t.Fatal(err)
		}
		// Fetch leaves deleted remote tags in the existing cache. Publication
		// checks must consult origin rather than treating the stale tag as live.
		testutil.Git(t, repo, "tag", "-d", "dependency/v2.0.0")
		pins, err := s.PublishedReleases("dependency")
		if err != nil || len(pins) != 1 || pins[0].Version != "1.0.0" {
			t.Fatalf("stale local tag counted as published: %+v, %v", pins, err)
		}
		// If all release tags disappear, a direct no-range install selects
		// the branch tip even though the cache still contains obsolete tags.
		testutil.Git(t, repo, "tag", "-d", "dependency/v1.0.0")
		p := build(t, m, []dependency.Target{{Name: "dependency", Source: "team"}}, dependency.NewGitSources(m.Sources))
		if n := node(t, p, "dependency"); n.Version != "" {
			t.Fatalf("obsolete cached tag used instead of branch tip: %+v", n)
		}
	})
}

func TestGitRequiresCachedCommitOfflineAfterFailedRefresh(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	commit := testutil.Release(t, repo, "a", "1.0.0")
	aliases := map[string]string{"team": testutil.URL(repo)}
	if _, err := source.Open("team", aliases["team"]); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	g := dependency.NewGitSources(aliases)
	if _, err := g.Releases("team", "a"); err == nil {
		t.Fatal("expected unavailable remote")
	}
	for i := 0; i < 2; i++ {
		if _, err := g.Requires("team", "a", commit); err != nil {
			t.Fatalf("cached commit requires a remote: %v", err)
		}
	}
	m := &manifest.Manifest{Sources: aliases, Skills: map[string]*manifest.Skill{"a": pin("team", "1.0.0", commit, true)}}
	m.Skills["a"].Resolved.Requires = nil
	build(t, m, nil, dependency.NewGitSources(aliases))
}

func TestGitPlannerInvalidReleaseMetadataFallsBackWithValidationContext(t *testing.T) {
	for _, validOlder := range []bool{true, false} {
		t.Run(fmt.Sprint(validOlder), func(t *testing.T) {
			testutil.IsolateHome(t)
			repo := testutil.NewRepo(t)
			if validOlder {
				testutil.Release(t, repo, "d", "1.0.0")
			}
			testutil.WriteFile(t, filepath.Join(repo, "skills", "d", "SKILL.md"), "---\nname: d\ndescription: invalid release\nmetadata:\n  version: 1.9.0\n  requires: [1]\n---\n")
			testutil.Commit(t, repo, "invalid d release")
			testutil.Git(t, repo, "tag", "d/v1.9.0")
			testutil.WriteSkillRequires(t, repo, "root", "1.0.0", []string{"d@^1"})
			testutil.Commit(t, repo, "root release")
			testutil.Git(t, repo, "tag", "root/v1.0.0")
			m := input()
			m.Sources["team"] = testutil.URL(repo)
			if validOlder {
				p := build(t, m, []dependency.Target{target("root")}, dependency.NewGitSources(m.Sources))
				if node(t, p, "d").Version != "1.0.0" {
					t.Fatalf("invalid newest release selected: %+v", p)
				}
			} else {
				err := failure(t, m, []dependency.Target{target("root")}, dependency.NewGitSources(m.Sources))
				var validation *dependency.ValidationError
				var access *dependency.AccessError
				if !errors.As(err, &validation) || errors.As(err, &access) {
					t.Fatalf("wrong error kind: %v", err)
				}
				for _, context := range []string{"team/d@1.9.0", "d/SKILL.md", "metadata.requires[0]"} {
					if !strings.Contains(err.Error(), context) {
						t.Fatalf("missing %s: %v", context, err)
					}
				}
			}
		})
	}
}
