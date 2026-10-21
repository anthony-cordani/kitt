package install_test

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func (f dependencyFixture) upgrade(t *testing.T, name string, major bool) string {
	t.Helper()
	var out bytes.Buffer
	opts := f.opts
	opts.Out = &out
	if err := install.Upgrade(opts, name, major); err != nil {
		t.Fatalf("Upgrade %q (major=%t): %v", name, major, err)
	}
	assertNoTempDirs(t, f.root)
	return out.String()
}

func TestUpgradeReplacesRequirementsAndRetainsSharedPinsAndOrphans(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "child", "1.0.0")
			dependencyRelease(t, f.repo, "removed", "1.0.0", "child@^1")
			dependencyRelease(t, f.repo, "changed", "1.0.0")
			dependencyRelease(t, f.repo, "shared", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "removed@^1", "changed@^1", "shared@^1")
			dependencyRelease(t, f.repo, "guard", "1.0.0", "shared@^1")
			f.add(t, "root")
			f.add(t, "guard")
			before := loadManifest(t, f.path)
			removedTree := snapshot(t, skillPath(f.root, "removed"))
			childTree := snapshot(t, skillPath(f.root, "child"))
			dependencyRelease(t, f.repo, "changed", "2.0.0")
			dependencyRelease(t, f.repo, "shared", "1.9.0")
			dependencyRelease(t, f.repo, "added", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.1.0", "changed@^2", "shared@^1", "added@^1")
			// HEAD differs from the selected release: requirements must come from its commit.
			testutil.WriteSkillRequires(t, f.repo, "root", "1.2.0", []string{"missing@^9"})
			testutil.Commit(t, f.repo, "unreleased root")

			out := f.upgrade(t, "root", false)
			m := loadManifest(t, f.path)
			assertResolved(t, m.Skills["root"], "^1.1.0", "1.1.0")
			assertResolved(t, m.Skills["changed"], "", "2.0.0")
			assertResolved(t, m.Skills["added"], "", "1.0.0")
			if !reflect.DeepEqual(m.Skills["shared"].Resolved, before.Skills["shared"].Resolved) || !slices.Equal(m.Skills["shared"].Via, []string{"guard", "root"}) {
				t.Fatalf("shared compatible pin moved or lost parents: %+v", m.Skills["shared"])
			}
			if m.Skills["removed"].IsDirect() || len(m.Skills["removed"].Via) != 0 || !slices.Equal(m.Skills["child"].Via, []string{"removed"}) {
				t.Fatal("orphan subtree was promoted or retained stale edges")
			}
			assertSameTree(t, removedTree, snapshot(t, skillPath(f.root, "removed")))
			assertSameTree(t, childTree, snapshot(t, skillPath(f.root, "child")))
			for _, n := range []string{"removed", "child", "added", "changed"} {
				assertLinkResolves(t, claudePath(f.root, n), skillPath(f.root, n))
			}
			for _, want := range []string{"upgraded changed 1.0.0 -> 2.0.0", "added dependency added 1.0.0", "dependency removed root -> demo/removed@^1", "dependency removed root -> demo/changed@^1", "dependency added root -> demo/changed@^2", "orphaned child (retained)", "orphaned removed (retained)"} {
				if !strings.Contains(out, want) {
					t.Fatalf("missing %q in output:\n%s", want, out)
				}
			}
		})
	}
}

func TestUpgradeMajorDirectWithoutDependents(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			f.add(t, "leaf@^1")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			f.upgrade(t, "leaf", true)
			assertResolved(t, loadManifest(t, f.path).Skills["leaf"], "^2.0.0", "2.0.0")
		})
	}
}

func TestUpgradeDependencyRefusalListsEveryBlocker(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, major := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/major=%t", global, major), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "leaf", "1.0.0")
				f.add(t, "leaf@^1")
				dependencyRelease(t, f.repo, "alpha", "1.0.0", "leaf@1.0.0")
				dependencyRelease(t, f.repo, "zulu", "1.0.0", "leaf@1.0.0")
				f.add(t, "alpha")
				f.add(t, "zulu")
				next := "1.1.0"
				if major {
					next = "2.0.0"
				}
				dependencyRelease(t, f.repo, "leaf", next)
				// Legacy ownership migration must also wait for graph validation.
				if err := os.Remove(skillPath(f.root, ".kitt-installed")); err != nil {
					t.Fatal(err)
				}
				before := f.snapshot(t)
				err := install.Upgrade(f.opts, "leaf", major)
				want := "cannot upgrade demo/leaf from 1.0.0 to " + next + "\n  demo/alpha@1.0.0 requires 1.0.0\n  demo/zulu@1.0.0 requires 1.0.0"
				assertUntouchedError(t, err, want)
				if err.Error() != want {
					t.Fatalf("error = %q, want %q", err.Error(), want)
				}
				assertSameTree(t, before, f.snapshot(t))
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestUpgradeMajorBulkMovesDependentAndDependencyTogether(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, dependencyFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/dependencyFirst=%t", global, dependencyFirst), func(t *testing.T) {
				f := dependencyScope(t, global)
				dep, parent := "alpha", "zulu"
				if !dependencyFirst {
					dep, parent = parent, dep
				}
				dependencyRelease(t, f.repo, dep, "1.0.0")
				f.add(t, dep+"@^1")
				dependencyRelease(t, f.repo, parent, "1.0.0", dep+"@^1")
				f.add(t, parent)
				dependencyRelease(t, f.repo, dep, "2.0.0")
				dependencyRelease(t, f.repo, parent, "2.0.0", dep+"@^2")
				before := f.snapshot(t)
				err := install.Upgrade(f.opts, dep, true)
				assertUntouchedError(t, err, "cannot upgrade demo/"+dep+" from 1.0.0 to 2.0.0\n  demo/"+parent+"@1.0.0 requires ^1")
				assertSameTree(t, before, f.snapshot(t))
				f.upgrade(t, "", true)
				m := loadManifest(t, f.path)
				for _, n := range []string{dep, parent} {
					assertResolved(t, m.Skills[n], "^2.0.0", "2.0.0")
				}
				if !slices.Equal(m.Skills[dep].Via, []string{parent}) {
					t.Fatalf("dependency lost parent: %+v", m.Skills[dep])
				}
			})
		}
	}
}

func TestUpgradeBulkValidatesFinalSharedDependencyGraph(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, compatible := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/compatible=%t", global, compatible), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "shared", "1.0.0")
				for _, n := range []string{"alpha", "zulu"} {
					dependencyRelease(t, f.repo, n, "1.0.0", "shared@^1")
					f.add(t, n)
				}
				dependencyRelease(t, f.repo, "shared", "2.0.0")
				dependencyRelease(t, f.repo, "alpha", "1.1.0", "shared@^2")
				rangeZulu := "^1"
				if compatible {
					rangeZulu = "^2"
				}
				dependencyRelease(t, f.repo, "zulu", "1.1.0", "shared@"+rangeZulu)
				before := f.snapshot(t)
				err := install.Upgrade(f.opts, "alpha", false)
				assertUntouchedError(t, err, "incompatible requirements for demo/shared")
				assertSameTree(t, before, f.snapshot(t))
				if !compatible {
					err = install.Upgrade(f.opts, "", false)
					assertUntouchedError(t, err, "demo/zulu@1.1.0 requires ^1")
					assertSameTree(t, before, f.snapshot(t))
					err = install.Upgrade(f.opts, "", true)
					assertUntouchedError(t, err, "demo/zulu@1.1.0 requires ^1")
					assertSameTree(t, before, f.snapshot(t))
					assertNoTempDirs(t, f.root)
					return
				}
				f.upgrade(t, "", false)
				m := loadManifest(t, f.path)
				assertResolved(t, m.Skills["shared"], "", "2.0.0")
				if m.Skills["shared"].IsDirect() || !slices.Equal(m.Skills["shared"].Via, []string{"alpha", "zulu"}) {
					t.Fatalf("shared = %+v", m.Skills["shared"])
				}
				for _, n := range []string{"alpha", "zulu"} {
					assertResolved(t, m.Skills[n], "^1.1.0", "1.1.0")
				}
			})
		}
	}
}

func TestUpgradeTransitiveOnlyNamedAndBoundedByDependents(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, major := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/major=%t", global, major), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "alpha", "1.0.0")
				dependencyRelease(t, f.repo, "zulu", "1.0.0", "alpha@^1")
				f.add(t, "zulu")
				dependencyRelease(t, f.repo, "alpha", "1.9.0")
				// An excluded release must not contribute its invalid requirements.
				dependencyRelease(t, f.repo, "alpha", "2.0.0", "unknown/forbidden@^9")
				out := f.upgrade(t, "", major)
				s := loadManifest(t, f.path).Skills["alpha"]
				assertResolved(t, s, "", "1.9.0")
				if major && !strings.Contains(out, "alpha 2.0.0 is available but demo/zulu@1.0.0 requires ^1") {
					t.Fatalf("missing blocked-major note: %s", out)
				}
				f.upgrade(t, "alpha", major)
				s = loadManifest(t, f.path).Skills["alpha"]
				assertResolved(t, s, "", "1.9.0")
				if s.IsDirect() || !slices.Equal(s.Via, []string{"zulu"}) {
					t.Fatalf("named upgrade promoted dependency: %+v", s)
				}
			})
		}
	}
}

func TestUpgradeMajorKeepsExactPins(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@1.0.0")
			f.add(t, "root@1.0.0")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			dependencyRelease(t, f.repo, "root", "2.0.0", "leaf@^2")
			before := f.snapshot(t)
			f.upgrade(t, "root", true)
			f.upgrade(t, "", true)
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}

func TestUpgradeWithoutMajorHonorsEditedExplicitConstraint(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			f.add(t, "leaf")
			dependencyRelease(t, f.repo, "leaf", "1.5.0")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			dependencyRelease(t, f.repo, "leaf", "2.5.0")
			m := loadManifest(t, f.path)
			m.Skills["leaf"].Version = "^2.1"
			f.save(t, m)
			f.upgrade(t, "leaf", false)
			assertResolved(t, loadManifest(t, f.path).Skills["leaf"], "^2.5.0", "2.5.0")
		})
	}
}

func TestUpgradeCyclesApplyNothing(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, bulk := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/bulk=%t", global, bulk), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "alpha", "1.0.0")
				dependencyRelease(t, f.repo, "zulu", "1.0.0")
				f.add(t, "alpha")
				f.add(t, "zulu")
				dependencyRelease(t, f.repo, "alpha", "1.1.0")
				dependencyRelease(t, f.repo, "zulu", "1.1.0", "zulu@^1")
				before := f.snapshot(t)
				name := "zulu"
				if bulk {
					name = ""
				}
				err := install.Upgrade(f.opts, name, false)
				assertUntouchedError(t, err, "dependency cycle: demo/zulu@1.1.0 -> demo/zulu@1.1.0")
				assertSameTree(t, before, f.snapshot(t))
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestInstallCaretCannotBypassDependentConstraint(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			before := f.snapshot(t)
			err := install.Add(f.opts, "leaf@^2")
			assertUntouchedError(t, err, "cannot install demo/leaf from 1.0.0 to 2.0.0\n  demo/root@1.0.0 requires ^1")
			assertSameTree(t, before, f.snapshot(t))
			assertNoTempDirs(t, f.root)
		})
	}
}
