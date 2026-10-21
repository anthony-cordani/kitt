package install

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func legacyRequirementsFixture(t *testing.T, requires string) (paths, Options) {
	t.Helper()
	p, opts, repo := transactionFixture(t, false)
	transactionRelease(t, repo, "legacy", "1.0.0")
	transactionRelease(t, repo, "other", "1.0.0")
	transactionRelease(t, repo, "parent", "1.0.0", "legacy@^1")
	if err := Add(opts, "legacy"); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: legacy\nmetadata:\n  requires: " + requires + "\n---\nlegacy content\n"
	testutil.WriteFile(t, filepath.Join(repo, "skills", "legacy", "SKILL.md"), body)
	sha := testutil.Commit(t, repo, "legacy non-kitt metadata")
	testutil.Git(t, repo, "tag", "-f", "legacy/v1.0.0")
	testutil.WriteFile(t, filepath.Join(p.skillsDir, "legacy", "SKILL.md"), body)
	m, err := manifest.Load(p.manifest)
	if err != nil {
		t.Fatal(err)
	}
	pin := m.Skills["legacy"].Resolved
	pin.Commit = sha
	pin.Hash, err = skill.Hash(filepath.Join(p.skillsDir, "legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Save(p.manifest); err != nil {
		t.Fatal(err)
	}
	return p, opts
}

func TestLegacyNonKittRequirementsAreUnknownOutsideClosure(t *testing.T) {
	for _, tc := range []struct{ name, requires string }{
		{"scalar", `"python3"`},
		{"foreign_list", `["numpy"]`},
	} {
		requires := tc.requires
		t.Run(tc.name, func(t *testing.T) {
			p, opts := legacyRequirementsFixture(t, requires)
			body, err := os.ReadFile(filepath.Join(p.skillsDir, "legacy", "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, restore := range []bool{false, true} {
				stderr := captureStderr(t)
				if restore {
					err = Restore(opts)
				} else {
					err = Add(opts, "other")
				}
				warning := stderr()
				if err != nil {
					t.Fatalf("restore=%t: %v", restore, err)
				}
				if strings.Count(warning, "\n") != 1 || !strings.Contains(warning, "warning: requirements for legacy are unknown:") {
					t.Fatalf("warning = %q", warning)
				}
				m, err := manifest.Load(p.manifest)
				if err != nil {
					t.Fatal(err)
				}
				if m.Skills["legacy"].Resolved.Requires != nil || m.Skills["legacy"].Direct != nil || m.Skills["other"].Resolved.Requires != nil {
					t.Fatal("unknown metadata changed dependency-free manifest format")
				}
				got, err := os.ReadFile(filepath.Join(p.skillsDir, "legacy", "SKILL.md"))
				if err != nil || string(got) != string(body) {
					t.Fatalf("legacy content changed: %s, %v", got, err)
				}
			}
		})
	}
}

func TestLegacyNonKittRequirementsFailWhenResolved(t *testing.T) {
	for _, tc := range []struct{ name, requires string }{
		{"scalar", `"python3"`},
		{"foreign_list", `["numpy"]`},
	} {
		requires := tc.requires
		for _, target := range []string{"legacy", "parent"} {
			t.Run(tc.name+"/"+target, func(t *testing.T) {
				p, opts := legacyRequirementsFixture(t, requires)
				before := transactionSnapshot(t, p)
				err := Add(opts, target)
				var pe *dependency.PlanError
				if err == nil || !errors.As(err, &pe) || !pe.ManagedStateUntouched || !strings.Contains(err.Error(), "metadata.requires") {
					t.Fatalf("validation = %v", err)
				}
				if after := transactionSnapshot(t, p); !reflect.DeepEqual(before, after) {
					t.Fatal("validation changed managed state")
				}
			})
		}
	}
}

func TestKittShapedRequiresFailRestoreUpgradeAndInstall(t *testing.T) {
	for _, mode := range []string{"restore", "upgrade", "install"} {
		t.Run(mode, func(t *testing.T) {
			p, opts := legacyRequirementsFixture(t, `["db@@1.0"]`)
			before := transactionSnapshot(t, p)
			stderr := captureStderr(t)
			var err error
			switch mode {
			case "restore":
				err = Restore(opts)
			case "upgrade":
				err = Upgrade(opts, "", false)
			default:
				err = Add(opts, "other")
			}
			warning := stderr()
			var pe *dependency.PlanError
			if err == nil || !errors.As(err, &pe) || !pe.ManagedStateUntouched || !strings.Contains(err.Error(), "db@@1.0") {
				t.Fatalf("%s = %v", mode, err)
			}
			if strings.Contains(warning, "warning:") {
				t.Fatalf("kitt-shaped requirements warned instead of failing: %s", warning)
			}
			if after := transactionSnapshot(t, p); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid requirements changed managed state")
			}
		})
	}
}

func TestInvalidRequiresAndVersionKeepFrontmatterErrors(t *testing.T) {
	for _, tc := range []struct{ name, requires string }{
		{"empty_list", `[]`},
		{"mixed_non_string", `["leaf@^1", 123]`},
		{"scalar", `"python3"`},
		{"foreign_list", `["numpy"]`},
		{"mapping", `{runtime: python3}`},
	} {
		requires := tc.requires
		for _, mode := range []string{"restore", "upgrade", "install"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				p, opts := legacyRequirementsFixture(t, requires)
				want := "metadata.requires[1]"
				if requires != `["leaf@^1", 123]` {
					path := filepath.Join(p.skillsDir, "legacy", "SKILL.md")
					body, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					testutil.WriteFile(t, path, strings.Replace(string(body), "metadata:\n", "metadata:\n  version: [1, 2]\n", 1))
					m, err := manifest.Load(p.manifest)
					if err != nil {
						t.Fatal(err)
					}
					m.Skills["legacy"].Resolved.Hash, err = skill.Hash(filepath.Join(p.skillsDir, "legacy"))
					if err != nil {
						t.Fatal(err)
					}
					if err := m.Save(p.manifest); err != nil {
						t.Fatal(err)
					}
					want = "metadata.version"
				}
				before := transactionSnapshot(t, p)
				stderr := captureStderr(t)
				var err error
				switch mode {
				case "restore":
					err = Restore(opts)
				case "upgrade":
					err = Upgrade(opts, "", false)
				default:
					err = Add(opts, "other")
				}
				warning := stderr()
				var pe *dependency.PlanError
				if err == nil || !errors.As(err, &pe) || !pe.ManagedStateUntouched || !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v; want %s", err, want)
				}
				if strings.Contains(warning, "warning:") {
					t.Fatalf("invalid metadata tolerated: %s", warning)
				}
				if after := transactionSnapshot(t, p); !reflect.DeepEqual(before, after) {
					t.Fatal("invalid frontmatter changed managed state")
				}
			})
		}
	}
}

func TestLegacyRequirementsSurviveProvisionalReplacement(t *testing.T) {
	p, _, repo := transactionFixture(t, false)
	transactionRelease(t, repo, "leaf", "1.0.0")
	transactionRelease(t, repo, "b", "1.0.0", "leaf@^1")
	bCommit := testutil.Git(t, repo, "rev-parse", "HEAD")
	transactionRelease(t, repo, "a", "1.0.0", "b@^1")
	aCommit := testutil.Git(t, repo, "rev-parse", "HEAD")
	transactionRelease(t, repo, "b", "2.0.0")
	m := &manifest.Manifest{Sources: map[string]string{"demo": testutil.URL(repo)}, Skills: map[string]*manifest.Skill{
		"a": {Source: "demo", Resolved: &manifest.Resolved{Version: "1.0.0", Commit: aCommit}},
		"b": {Source: "demo", Resolved: &manifest.Resolved{Version: "1.0.0", Commit: bCommit}},
	}}
	access := newTestInstallSources(m.Sources)
	access.current, access.paths = m, p
	targets := []dependency.Target{
		{Name: "a", Policy: dependency.Upgrade, BoundByDependents: true},
		{Name: "b", Policy: dependency.Upgrade, BoundByDependents: true},
	}
	current, err := planningManifest(p, m, access, targets...)
	if err != nil {
		t.Fatal(err)
	}
	provisional := buildGitPlan(t, current, targets, access)
	if n := gitPlanNode(t, provisional, "b"); n.Version != "2.0.0" {
		t.Fatalf("b was not provisionally moved: %+v", n)
	}
	plan, err := buildDependencyPlan(p, current, targets, access, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	b := gitPlanNode(t, plan, "b")
	if b.Commit != bCommit || !reflect.DeepEqual(b.Requires, []string{"demo/leaf@^1"}) {
		t.Fatalf("restored b lost requirements: %+v", b)
	}
	if leaf := gitPlanNode(t, plan, "leaf"); !reflect.DeepEqual(leaf.Via, []string{"b"}) {
		t.Fatalf("downstream requirement missing: %+v", leaf)
	}
	if len(access.unknown) != 0 || !access.legacy["a"] || !access.legacy["b"] {
		t.Fatalf("legacy=%v unknown=%v", access.legacy, access.unknown)
	}
}

func TestUnknownLegacyRequirementsWarning(t *testing.T) {
	const both = "warning: requirements of a, b are unknown (not installed); run kitt install to restore them, then kitt doctor to verify\n"
	for _, tc := range []struct{ name, warning string }{
		{"install", both},
		{"upgrade", both},
		{"unchanged", ""},
		{"new_pin", ""},
		{"no_dependencies", ""},
		{"known_snapshots", ""},
		{"verified_trees", ""},
		{"recorded_parents", ""},
		{"replaced_legacy", strings.Replace(both, "a, b", "b", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, opts, repo := transactionFixture(t, false)
			transactionRelease(t, repo, "child", "1.0.0")
			var requires []string
			if tc.name != "no_dependencies" {
				requires = []string{"child@^1"}
			}
			for _, name := range []string{"a", "b"} {
				transactionRelease(t, repo, name, "1.0.0", requires...)
				if err := Add(opts, name); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "no_dependencies" {
				if err := Add(opts, "child"); err != nil {
					t.Fatal(err)
				}
			}
			m, err := manifest.Load(p.manifest)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "b"} {
				if tc.name != "known_snapshots" {
					m.Skills[name].Resolved.Requires = nil
				}
				if tc.name != "verified_trees" {
					if name == "a" && tc.name != "no_dependencies" {
						if err := os.RemoveAll(filepath.Join(p.skillsDir, name)); err != nil {
							t.Fatal(err)
						}
					} else {
						testutil.WriteFile(t, filepath.Join(p.skillsDir, name, "SKILL.md"), "local edit\n")
					}
				}
			}
			if tc.name != "recorded_parents" {
				m.Skills["child"].Via = nil
			}
			if err := m.Save(p.manifest); err != nil {
				t.Fatal(err)
			}
			if tc.name != "unchanged" {
				transactionRelease(t, repo, "child", "1.1.0")
			}
			target := "child@^1"
			if tc.name == "upgrade" {
				target = "child"
			} else if tc.name == "new_pin" {
				transactionRelease(t, repo, "other", "1.0.0")
				target = "other"
			} else if tc.name == "replaced_legacy" {
				transactionRelease(t, repo, "a", "2.0.0", requires...)
				target = "a@^2"
			}
			var out strings.Builder
			opts.Out = &out
			stderr := captureStderr(t)
			if tc.name == "upgrade" {
				err = Upgrade(opts, target, false)
			} else {
				err = Add(opts, target)
			}
			warning := stderr()
			if err != nil {
				t.Fatal(err)
			}
			if warning != tc.warning {
				t.Fatalf("stderr = %q; want %q", warning, tc.warning)
			}
			if tc.name == "no_dependencies" && out.String() != "added child 1.1.0\n" {
				t.Fatalf("dependency-free output differs from main: %q", out.String())
			}
			m, err = manifest.Load(p.manifest)
			if err != nil {
				t.Fatal(err)
			}
			if tc.warning == both {
				if m.Skills["child"].Resolved.Version != "1.1.0" || m.Skills["a"].Resolved.Requires != nil || m.Skills["b"].Resolved.Requires != nil {
					t.Fatalf("warning changed pins or unknown snapshots: %+v", m.Skills)
				}
			}
		})
	}
}
