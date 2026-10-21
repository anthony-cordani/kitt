package install_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

type dependencyFixture struct {
	root, repo, path string
	opts             install.Options
}

func dependencyScope(t *testing.T, global bool) dependencyFixture {
	t.Helper()
	home := testutil.IsolateHome(t)
	project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
	root, path := project, filepath.Join(project, manifest.FileName)
	if global {
		root = home
		var err error
		path, err = manifest.GlobalPath()
		if err != nil {
			t.Fatal(err)
		}
	}
	testutil.WriteFile(t, path, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	return dependencyFixture{root: root, repo: repo, path: path, opts: install.Options{Root: project, Global: global}}
}

func dependencyRelease(t *testing.T, repo, name, version string, requires ...string) {
	t.Helper()
	testutil.WriteSkillRequires(t, repo, name, version, requires)
	testutil.Commit(t, repo, name+" "+version)
	testutil.Git(t, repo, "tag", name+"/v"+version)
}

func (f dependencyFixture) add(t *testing.T, ref string) {
	t.Helper()
	if err := install.Add(f.opts, ref); err != nil {
		t.Fatalf("Add %s: %v", ref, err)
	}
}

func (f dependencyFixture) restore(t *testing.T) {
	t.Helper()
	if err := install.Restore(f.opts); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

func (f dependencyFixture) save(t *testing.T, m *manifest.Manifest) {
	t.Helper()
	if err := m.Save(f.path); err != nil {
		t.Fatal(err)
	}
}

// Global source caches may change during planning. Snapshot only managed state.
// snapshot records symlinks and Windows junctions by target instead of reading them.
func (f dependencyFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	got := map[string]string{"manifest": readText(t, f.path)}
	for _, dir := range []string{".agents", ".claude"} {
		path := filepath.Join(f.root, dir)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		got[dir] = "dir"
		for path, value := range snapshot(t, path) {
			got[filepath.Join(dir, path)] = value
		}
	}
	return got
}

func removeSourceAndCache(t *testing.T, repo string) {
	t.Helper()
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cache, "kitt")); err != nil {
		t.Fatal(err)
	}
}

func assertUntouchedError(t *testing.T, err error, contains string) {
	t.Helper()
	var pe *dependency.PlanError
	if err == nil || !strings.Contains(err.Error(), contains) || !errors.As(err, &pe) || !pe.ManagedStateUntouched {
		t.Fatalf("error = %v; want untouched PlanError containing %q", err, contains)
	}
}

func TestDependencyInstallGraph(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, diamond := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/diamond=%t", global, diamond), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "leaf", "1.0.0")
				dependencyRelease(t, f.repo, "left", "1.0.0", "leaf@^1")
				requires := []string{"left@^1"}
				parents := []string{"left"}
				if diamond {
					dependencyRelease(t, f.repo, "right", "1.0.0", "leaf@^1.0")
					requires = append(requires, "demo/right@^1")
					parents = append(parents, "right")
				}
				dependencyRelease(t, f.repo, "root", "1.0.0", requires...)
				f.add(t, "root")
				m := loadManifest(t, f.path)
				if !slices.Equal(m.Skills["leaf"].Via, parents) {
					t.Fatalf("leaf via = %v, want %v", m.Skills["leaf"].Via, parents)
				}
				for name, s := range m.Skills {
					if s.IsDirect() != (name == "root") || (name != "root" && (s.Direct == nil || s.Version != "")) {
						t.Fatalf("direct/constraint for %s: %+v", name, s)
					}
					if s.Resolved == nil || s.Resolved.Requires == nil {
						t.Fatalf("%s missing pin snapshot: %+v", name, s)
					}
					if name == "leaf" && len(*s.Resolved.Requires) != 0 {
						t.Fatalf("leaf requires = %v", *s.Resolved.Requires)
					}
					for _, ref := range *s.Resolved.Requires {
						if !strings.HasPrefix(ref, "demo/") {
							t.Fatalf("unnormalized requirement %q", ref)
						}
					}
					assertLinkResolves(t, claudePath(f.root, name), skillPath(f.root, name))
					sum, err := skill.Hash(skillPath(f.root, name))
					if err != nil || sum != s.Resolved.Hash {
						t.Fatalf("installed %s hash = %s, err %v", name, sum, err)
					}
				}
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestDependencyDirectPromotionKeepsPinAndVia(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			m := loadManifest(t, f.path)
			pin := *m.Skills["leaf"].Resolved
			m.Sources["other"] = "file:///unreachable-unused"
			f.save(t, m)
			dependencyRelease(t, f.repo, "leaf", "1.9.0")
			removeSourceAndCache(t, f.repo)
			f.add(t, "leaf")
			s := loadManifest(t, f.path).Skills["leaf"]
			if !s.IsDirect() || s.Version != "" || !slices.Equal(s.Via, []string{"root"}) || !reflect.DeepEqual(*s.Resolved, pin) {
				t.Fatalf("promotion changed pin/graph: %+v, pin %+v", s, s.Resolved)
			}
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestDependencyUnresolvedRestoreKeepsExistingPins(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "shared", "1.0.0")
			f.add(t, "shared")
			old := *loadManifest(t, f.path).Skills["shared"].Resolved
			leaf := []string{}
			old.Requires = &leaf
			dependencyRelease(t, f.repo, "shared", "1.9.0")
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "shared@^1", "leaf@^1")
			m := loadManifest(t, f.path)
			m.Skills["root"] = &manifest.Skill{Source: "demo"}
			f.save(t, m)
			f.restore(t)
			m = loadManifest(t, f.path)
			if !reflect.DeepEqual(*m.Skills["shared"].Resolved, old) {
				t.Fatal("restore moved an existing pin")
			}
			assertResolved(t, m.Skills["root"], "^1.0.0", "1.0.0")
			if m.Skills["leaf"].IsDirect() || m.Skills["leaf"].Version != "" {
				t.Fatalf("leaf = %+v", m.Skills["leaf"])
			}
		})
	}
}

func TestDependencyManifestOnlyCloneRestoresExactPins(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			before := readText(t, f.path)
			dependencyRelease(t, f.repo, "leaf", "1.9.0")
			dependencyRelease(t, f.repo, "root", "2.0.0", "missing@^2")
			if global {
				for _, dir := range []string{".agents", ".claude"} {
					if err := os.RemoveAll(filepath.Join(f.root, dir)); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				f.root = testutil.NewRepo(t)
				f.opts.Root = f.root
				f.path = filepath.Join(f.root, manifest.FileName)
				testutil.WriteFile(t, f.path, before)
			}
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(cache, "kitt")); err != nil {
				t.Fatal(err)
			}
			f.restore(t)
			if readText(t, f.path) != before {
				t.Fatal("exact restore rewrote manifest")
			}
			for _, name := range []string{"root", "leaf"} {
				s := loadManifest(t, f.path).Skills[name]
				if s.Resolved.Version != "1.0.0" {
					t.Fatalf("%s moved to %s", name, s.Resolved.Version)
				}
				assertLinkResolves(t, claudePath(f.root, name), skillPath(f.root, name))
			}
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestDependencyCacheOnlyReinstall(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, modified := range []bool{false, true} {
			t.Run(fmt.Sprintf("global=%t/modified=%t", global, modified), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "leaf", "1.0.0")
				dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
				f.add(t, "root")
				m := readText(t, f.path)
				if err := os.RemoveAll(f.repo); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"root", "leaf"} {
					if modified {
						testutil.WriteFile(t, filepath.Join(skillPath(f.root, name), "SKILL.md"), "foreign modified metadata\n")
					} else if err := os.RemoveAll(skillPath(f.root, name)); err != nil {
						t.Fatal(err)
					}
				}
				f.restore(t)
				if readText(t, f.path) != m {
					t.Fatal("cache-only restore changed manifest")
				}
				for name, s := range loadManifest(t, f.path).Skills {
					sum, err := skill.Hash(skillPath(f.root, name))
					if err != nil || sum != s.Resolved.Hash {
						t.Fatalf("%s was not restored to its pin", name)
					}
				}
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestDependencyUnchangedRestoreWithoutSourceOrCache(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			removeSourceAndCache(t, f.repo)
			before := f.snapshot(t)
			f.restore(t)
			assertSameTree(t, before, f.snapshot(t))
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			assertMissing(t, filepath.Join(cache, "kitt"))
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestDependencyRestoreRejectsStaleAndIncompleteManifest(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, edit := range []string{"version", "missing", "requires"} {
			t.Run(fmt.Sprintf("global=%t/%s", global, edit), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "leaf", "1.0.0")
				dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
				f.add(t, "root")
				m := loadManifest(t, f.path)
				want := ""
				switch edit {
				case "version":
					m.Skills["root"].Version = "^2"
					want = "skills.root.version"
				case "missing":
					delete(m.Skills, "leaf")
					want = "incomplete manifest"
				case "requires":
					raw := []string{"demo/leaf@^2"}
					m.Skills["root"].Resolved.Requires = &raw
					want = "explicit install or upgrade"
				}
				f.save(t, m)
				removeSourceAndCache(t, f.repo)
				before := f.snapshot(t)
				err := install.Restore(f.opts)
				assertUntouchedError(t, err, want)
				if edit == "version" && (!strings.Contains(err.Error(), "1.0.0") || !strings.Contains(err.Error(), "clear skills.root.resolved")) {
					t.Fatalf("stale pin recovery = %v", err)
				}
				assertSameTree(t, before, f.snapshot(t))
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestDependencyOrphanRetention(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root@1.0.0")
			pin := *loadManifest(t, f.path).Skills["leaf"].Resolved
			dependencyRelease(t, f.repo, "root", "2.0.0")
			f.add(t, "root@2.0.0")
			f.restore(t)
			leaf := loadManifest(t, f.path).Skills["leaf"]
			if leaf == nil || leaf.IsDirect() || len(leaf.Via) != 0 || !reflect.DeepEqual(*leaf.Resolved, pin) {
				t.Fatalf("retained orphan must remain transitive after the last edge disappears: %+v", leaf)
			}
			assertLinkResolves(t, claudePath(f.root, "leaf"), skillPath(f.root, "leaf"))
			_, doctor := fixtureDoctor(t, f)
			if !strings.Contains(doctor, "orphaned dependency: leaf") {
				t.Fatalf("orphan not reported: %s", doctor)
			}
			var list strings.Builder
			opts := f.opts
			opts.Out = &list
			if err := install.List(opts); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(list.String(), "Orphaned transitive skills:") || !strings.Contains(list.String(), "demo/leaf@1.0.0") {
				t.Fatalf("orphan not listed: %s", &list)
			}
		})
	}
}

func TestDependencyPlanningFailuresLeaveManagedStateUntouched(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, failure := range []string{"conflict", "cycle", "foreign"} {
			t.Run(fmt.Sprintf("global=%t/%s", global, failure), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "shared", "1.0.0")
				f.add(t, "shared@1.0.0")
				if err := os.Remove(skillPath(f.root, ".kitt-installed")); err != nil {
					t.Fatal(err)
				}
				want := ""
				switch failure {
				case "conflict":
					dependencyRelease(t, f.repo, "root", "1.0.0", "shared@^2")
					want = "incompatible requirements"
				case "cycle":
					dependencyRelease(t, f.repo, "child", "1.0.0", "root@^1")
					dependencyRelease(t, f.repo, "root", "1.0.0", "child@^1")
					want = "dependency cycle"
				case "foreign":
					dependencyRelease(t, f.repo, "child", "1.0.0")
					dependencyRelease(t, f.repo, "root", "1.0.0", "child@^1")
					testutil.WriteFile(t, filepath.Join(skillPath(f.root, "child"), "note"), "foreign\n")
					want = "was not installed by kitt"
				}
				before := f.snapshot(t)
				err := install.Add(f.opts, "root")
				assertUntouchedError(t, err, want)
				assertSameTree(t, before, f.snapshot(t))
				assertMissing(t, skillPath(f.root, ".kitt-installed"))
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestDependencyStagingValidationLeavesManagedStateUntouched(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, failure := range []string{"hash", "snapshot", "link-path"} {
			t.Run(fmt.Sprintf("global=%t/%s", global, failure), func(t *testing.T) {
				f := dependencyScope(t, global)
				dependencyRelease(t, f.repo, "alpha", "1.0.0")
				dependencyRelease(t, f.repo, "zulu", "1.0.0")
				f.add(t, "alpha")
				f.add(t, "zulu")
				m := loadManifest(t, f.path)
				want := ""
				switch failure {
				case "hash":
					m.Skills["zulu"].Resolved.Hash = "h1:incorrect"
					want = "hash mismatch"
				case "snapshot":
					raw := []string{"demo/alpha@^1"}
					m.Skills["zulu"].Resolved.Requires = &raw
					want = "snapshot disagrees"
				case "link-path":
					if err := os.RemoveAll(filepath.Join(f.root, ".claude", "skills")); err != nil {
						t.Fatal(err)
					}
					testutil.WriteFile(t, filepath.Join(f.root, ".claude", "skills"), "not a directory\n")
					want = "not a directory"
				}
				f.save(t, m)
				for _, name := range []string{"alpha", "zulu"} {
					if err := os.RemoveAll(skillPath(f.root, name)); err != nil {
						t.Fatal(err)
					}
				}
				before := f.snapshot(t)
				err := install.Restore(f.opts)
				assertUntouchedError(t, err, want)
				assertSameTree(t, before, f.snapshot(t))
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestDependencyLegacyManifestRemainsByteIdenticalOffline(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			testutil.Release(t, f.repo, "legacy", "1.0.0")
			f.add(t, "legacy")
			m := loadManifest(t, f.path)
			m.Skills["legacy"].Resolved.Requires = nil
			f.save(t, m)
			text := "# Preserve comments and spacing in legacy manifests.\n\n" + readText(t, f.path)
			testutil.WriteFile(t, f.path, text)
			removeSourceAndCache(t, f.repo)
			before := f.snapshot(t)
			f.restore(t)
			if readText(t, f.path) != text {
				t.Fatal("unchanged legacy manifest was rewritten")
			}
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}

func TestDependencyUnresolvedRestoreCannotMovePinnedDependency(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			dependencyRelease(t, f.repo, "new-root", "1.0.0", "leaf@^2")
			m := loadManifest(t, f.path)
			m.Skills["new-root"] = &manifest.Skill{Source: "demo"}
			f.save(t, m)
			before := f.snapshot(t)
			err := install.Restore(f.opts)
			assertUntouchedError(t, err, "explicit install or upgrade")
			assertSameTree(t, before, f.snapshot(t))
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestDependencyExplicitInstallProtectsDependents(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			f.add(t, "root")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			before := f.snapshot(t)
			err := install.Add(f.opts, "leaf@2.0.0")
			assertUntouchedError(t, err, "cannot install demo/leaf")
			if !strings.Contains(err.Error(), "demo/root@1.0.0 requires ^1") {
				t.Fatalf("missing requester: %v", err)
			}
			assertSameTree(t, before, f.snapshot(t))
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestAddLeavesUnrelatedModifiedPinsOffline(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%t/missing=%t", legacy, missing), func(t *testing.T) {
				f := dependencyScope(t, false)
				dependencyRelease(t, f.repo, "notes", "1.0.0")
				f.add(t, "notes")
				m := loadManifest(t, f.path)
				if legacy {
					m.Skills["notes"].Resolved.Requires = nil
				}
				otherRepo := testutil.NewRepo(t)
				dependencyRelease(t, otherRepo, "other", "1.0.0")
				m.Sources["other"] = testutil.URL(otherRepo)
				f.save(t, m)
				if missing {
					if err := os.RemoveAll(skillPath(f.root, "notes")); err != nil {
						t.Fatal(err)
					}
				} else {
					testutil.WriteFile(t, filepath.Join(skillPath(f.root, "notes"), "SKILL.md"), "my local notes\n")
				}
				removeSourceAndCache(t, f.repo)
				f.add(t, "other/other")
				if missing {
					assertMissing(t, skillPath(f.root, "notes"))
				} else if got := readText(t, filepath.Join(skillPath(f.root, "notes"), "SKILL.md")); got != "my local notes\n" {
					t.Fatalf("notes overwritten: %q", got)
				}
				if !reflect.DeepEqual(m.Skills["notes"], loadManifest(t, f.path).Skills["notes"]) {
					t.Fatal("unrelated pin changed")
				}
			})
		}
	}
}

func TestRestoreBacktracksToKeepEveryPin(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	f.add(t, "leaf")
	dependencyRelease(t, f.repo, "leaf", "2.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	dependencyRelease(t, f.repo, "root", "2.0.0", "leaf@^2")
	m := loadManifest(t, f.path)
	pin := *m.Skills["leaf"].Resolved
	leaf := []string{}
	pin.Requires = &leaf
	m.Skills["root"] = &manifest.Skill{Source: "demo"}
	f.save(t, m)
	f.restore(t)
	m = loadManifest(t, f.path)
	if m.Skills["root"].Resolved.Version != "1.0.0" || !reflect.DeepEqual(pin, *m.Skills["leaf"].Resolved) {
		t.Fatalf("unexpected pins: %+v", m.Skills)
	}
}

func TestAddRejectsUnrelatedStalePin(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "stale", "1.0.0")
	f.add(t, "stale")
	dependencyRelease(t, f.repo, "stale", "2.0.0")
	dependencyRelease(t, f.repo, "other", "1.0.0")
	m := loadManifest(t, f.path)
	m.Skills["stale"].Version = "^2"
	f.save(t, m)
	before := f.snapshot(t)
	assertUntouchedError(t, install.Add(f.opts, "other"), "skills.stale.version")
	assertSameTree(t, before, f.snapshot(t))
	f.add(t, "stale@^2")
}

func TestAddEmptyConstraintAndUnchangedOutput(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "foo", "1.0.0")
	dependencyRelease(t, f.repo, "foo", "2.0.0")
	f.add(t, "demo/foo@")
	if got := loadManifest(t, f.path).Skills["foo"].Resolved.Version; got != "2.0.0" {
		t.Fatalf("got %s", got)
	}
	before := f.snapshot(t)
	var out strings.Builder
	f.opts.Out = &out
	f.add(t, "foo")
	if !strings.Contains(out.String(), "added foo 2.0.0") || strings.Contains(out.String(), "up to date foo") {
		t.Fatalf("output: %s", out.String())
	}
	assertSameTree(t, before, f.snapshot(t))
}

func TestAddLoadsLegacyRequestedClosure(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "child", "1.0.0", "leaf@^1")
	f.add(t, "child")
	m := loadManifest(t, f.path)
	m.Skills["child"].Resolved.Requires = nil
	m.Skills["leaf"].Resolved.Requires = nil
	f.save(t, m)
	dependencyRelease(t, f.repo, "root", "1.0.0", "child@^1")
	f.add(t, "root")
	m = loadManifest(t, f.path)
	if m.Skills["child"].Resolved.Requires == nil || !slices.Equal(m.Skills["leaf"].Via, []string{"child"}) {
		t.Fatal("legacy closure not loaded")
	}
}

func TestUpgradeRejectsUnrelatedStalePin(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "stale", "1.0.0")
	dependencyRelease(t, f.repo, "other", "1.0.0")
	f.add(t, "stale")
	f.add(t, "other")
	dependencyRelease(t, f.repo, "stale", "2.0.0")
	dependencyRelease(t, f.repo, "other", "1.1.0")
	m := loadManifest(t, f.path)
	m.Skills["stale"].Version = "^2"
	f.save(t, m)
	before := f.snapshot(t)
	assertUntouchedError(t, install.Upgrade(f.opts, "other", false), "skills.stale.version")
	assertSameTree(t, before, f.snapshot(t))
	if err := install.Upgrade(f.opts, "stale", false); err != nil {
		t.Fatalf("explicit stale target: %v", err)
	}
	if got := loadManifest(t, f.path).Skills["stale"].Resolved.Version; got != "2.0.0" {
		t.Fatalf("explicit target pin: %s", got)
	}
}

func TestSymlinkedClaudeSkillsDirPreservesSkillContent(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("global=%t", global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "foo", "1.0.0")
			dependencyRelease(t, f.repo, "other", "1.0.0")
			f.add(t, "foo")
			foo := readText(t, filepath.Join(skillPath(f.root, "foo"), "SKILL.md"))
			replaceClaudeSkillsWithLink(t, f.root)
			f.restore(t)
			assertSkillKept(t, f.root, "foo", foo)
			f.add(t, "other")
			assertSkillKept(t, f.root, "foo", foo)
			other := readText(t, filepath.Join(skillPath(f.root, "other"), "SKILL.md"))
			dependencyRelease(t, f.repo, "foo", "1.1.0")
			f.upgrade(t, "", false)
			upgraded := readText(t, filepath.Join(skillPath(f.root, "foo"), "SKILL.md"))
			if !strings.Contains(upgraded, "1.1.0") {
				t.Fatalf("foo was not upgraded:\n%s", upgraded)
			}
			assertSkillKept(t, f.root, "other", other)
			parent := filepath.Join(f.root, ".claude", "skills")
			if got, err := os.Readlink(parent); err != nil || filepath.Clean(got) != filepath.FromSlash("../.agents/skills") {
				t.Fatalf("skills parent link = %q, %v", got, err)
			}
		})
	}
}

func TestCorrectManagedLinkIsNotRecreated(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprintf("global=%t", global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "foo", "1.0.0")
			dependencyRelease(t, f.repo, "other", "1.0.0")
			f.add(t, "foo")
			link := claudePath(f.root, "foo")
			before, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			target, err := os.Readlink(link)
			if err != nil {
				t.Fatal(err)
			}
			f.add(t, "other")
			assertLinkUntouched(t, link, before, target)
			f.restore(t)
			assertLinkUntouched(t, link, before, target)
			dependencyRelease(t, f.repo, "foo", "1.1.0")
			f.upgrade(t, "", false)
			assertLinkUntouched(t, link, before, target)
		})
	}
}

func TestInstallRepairsRequestedSkillWhenTreeIsMissingOrModified(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, ref := range []string{"foo", "foo@^2"} {
			t.Run(fmt.Sprintf("missing=%t/%s", missing, ref), func(t *testing.T) {
				f := dependencyScope(t, false)
				dependencyRelease(t, f.repo, "foo", "2.0.0")
				dependencyRelease(t, f.repo, "notes", "1.0.0")
				f.add(t, "foo")
				f.add(t, "notes")
				pinned := loadManifest(t, f.path).Skills["foo"].Resolved.Hash
				notes := filepath.Join(skillPath(f.root, "notes"), "SKILL.md")
				testutil.WriteFile(t, notes, "local notes\n")
				foo := skillPath(f.root, "foo")
				if missing {
					if err := os.RemoveAll(foo); err != nil {
						t.Fatal(err)
					}
				} else {
					testutil.WriteFile(t, filepath.Join(foo, "SKILL.md"), "edited\n")
				}
				f.add(t, ref)
				sum, err := skill.Hash(foo)
				if err != nil || sum != pinned {
					t.Fatalf("restored hash = %s, err %v, want %s", sum, err, pinned)
				}
				assertLinkResolves(t, claudePath(f.root, "foo"), foo)
				if got := readText(t, notes); got != "local notes\n" {
					t.Fatalf("unrelated skill changed: %q", got)
				}
			})
		}
	}
}

func TestLegacyVerifiedParentProtectsSharedDependency(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
	f.add(t, "a")
	m := loadManifest(t, f.path)
	m.Skills["a"].Resolved.Requires = nil
	f.save(t, m)
	dependencyRelease(t, f.repo, "leaf", "2.0.0")
	before := f.snapshot(t)
	err := install.Add(f.opts, "leaf@^2")
	assertUntouchedError(t, err, "cannot install demo/leaf")
	if !strings.Contains(err.Error(), "demo/a@1.0.0 requires ^1") {
		t.Fatalf("missing requester: %v", err)
	}
	assertSameTree(t, before, f.snapshot(t))
}

func TestLegacyVerifiedParentKeepsDependencyOnUnrelatedInstall(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
	f.add(t, "a")
	m := loadManifest(t, f.path)
	m.Skills["a"].Resolved.Requires = nil
	otherRepo := testutil.NewRepo(t)
	dependencyRelease(t, otherRepo, "other", "1.0.0")
	m.Sources["other"] = testutil.URL(otherRepo)
	f.save(t, m)
	var out strings.Builder
	f.opts.Out = &out
	f.add(t, "other/other")
	if strings.Contains(out.String(), "orphaned") {
		t.Fatalf("output rewrote the graph:\n%s", out.String())
	}
	leaf := loadManifest(t, f.path).Skills["leaf"]
	if leaf.IsDirect() || !slices.Equal(leaf.Via, []string{"a"}) || leaf.Resolved.Version != "1.0.0" {
		t.Fatalf("leaf = %+v resolved %+v", leaf, leaf.Resolved)
	}
}

func TestUnknownLegacyRequirementsDoNotRewriteViaOrOrphan(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
			f.add(t, "a")
			m := loadManifest(t, f.path)
			m.Skills["a"].Resolved.Requires = nil
			if missing {
				if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md"), "local edit\n")
			}
			otherRepo := testutil.NewRepo(t)
			dependencyRelease(t, otherRepo, "other", "1.0.0")
			m.Sources["other"] = testutil.URL(otherRepo)
			f.save(t, m)
			removeSourceAndCache(t, f.repo)
			var out strings.Builder
			f.opts.Out = &out
			f.add(t, "other/other")
			if strings.Contains(out.String(), "orphaned") {
				t.Fatalf("unknown requirements orphaned a skill:\n%s", out.String())
			}
			got := loadManifest(t, f.path)
			leaf := got.Skills["leaf"]
			if leaf.IsDirect() || !slices.Equal(leaf.Via, []string{"a"}) || got.Skills["a"].Resolved.Requires != nil {
				t.Fatalf("leaf = %+v, a.requires = %v", leaf, got.Skills["a"].Resolved.Requires)
			}
			if missing {
				assertMissing(t, skillPath(f.root, "a"))
			} else if text := readText(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md")); text != "local edit\n" {
				t.Fatalf("modified skill restored: %q", text)
			}
		})
	}
}

func replaceClaudeSkillsWithLink(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "skills")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../.agents/skills", dir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlink requires Windows privilege")
		}
		t.Fatal(err)
	}
}

func assertSkillKept(t *testing.T, root, name, want string) {
	t.Helper()
	for _, dir := range []string{skillPath(root, name), claudePath(root, name)} {
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatalf("lstat %s: %v", dir, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("%s is a self-referencing link: %v", dir, err)
			}
		}
	}
	if got := readText(t, filepath.Join(skillPath(root, name), "SKILL.md")); got != want {
		t.Fatalf("%s content = %q, want %q", name, got, want)
	}
}

func assertLinkUntouched(t *testing.T, path string, before os.FileInfo, target string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, info) || got != target {
		t.Fatalf("%s was recreated (same=%v, target %q, want %q)", path, os.SameFile(before, info), got, target)
	}
}

func TestLegacyParentPinnedRequirementsBlockIncompatibleInstall(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
			f.add(t, "a")
			m := loadManifest(t, f.path)
			m.Skills["a"].Resolved.Requires = nil
			f.save(t, m)
			if missing {
				if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md"), "local edit\n")
			}
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			// Neither the latest release nor HEAD describes the pinned parent's range.
			dependencyRelease(t, f.repo, "a", "2.0.0", "leaf@^2")
			before := f.snapshot(t)
			err := install.Add(f.opts, "leaf@^2")
			assertUntouchedError(t, err, "cannot install demo/leaf from 1.0.0 to 2.0.0\n  demo/a@1.0.0 requires ^1")
			var access *dependency.AccessError
			if errors.As(err, &access) {
				t.Fatalf("conflict reported as source failure: %v", err)
			}
			assertSameTree(t, before, f.snapshot(t))
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestLegacyParentTargetUsesSelectedRequirements(t *testing.T) {
	for _, missing := range []bool{false, true} {
		for _, operation := range []string{"install", "upgrade"} {
			t.Run(fmt.Sprintf("missing=%t/%s", missing, operation), func(t *testing.T) {
				f := dependencyScope(t, false)
				dependencyRelease(t, f.repo, "leaf", "1.0.0")
				dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
				f.add(t, "a")
				m := loadManifest(t, f.path)
				m.Skills["a"].Resolved.Requires = nil
				f.save(t, m)
				if missing {
					if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
						t.Fatal(err)
					}
				} else {
					testutil.WriteFile(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md"), "local edit\n")
				}
				dependencyRelease(t, f.repo, "leaf", "2.0.0")
				dependencyRelease(t, f.repo, "a", "2.0.0", "leaf@^2")
				if operation == "install" {
					f.add(t, "a@^2")
				} else if err := install.Upgrade(f.opts, "a", true); err != nil {
					t.Fatalf("upgrade --major a: %v", err)
				}
				got := loadManifest(t, f.path)
				a, leaf := got.Skills["a"], got.Skills["leaf"]
				if a.Resolved == nil || leaf.Resolved == nil || a.Resolved.Version != "2.0.0" || leaf.Resolved.Version != "2.0.0" || !slices.Equal(leaf.Via, []string{"a"}) {
					t.Fatalf("a=%+v resolved %+v; leaf=%+v resolved %+v", a, a.Resolved, leaf, leaf.Resolved)
				}
				body := readText(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md"))
				if strings.Contains(body, "local edit") || !strings.Contains(body, "2.0.0") {
					t.Fatalf("parent was not reinstalled:\n%s", body)
				}
				assertNoTempDirs(t, f.root)
			})
		}
	}
}

func TestBulkUpgradeLegacyParentAllowsCompatibleDependency(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
			f.add(t, "a")
			m := loadManifest(t, f.path)
			m.Skills["a"].Resolved.Requires = nil
			f.save(t, m)
			if missing {
				if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md"), "local edit\n")
			}
			dependencyRelease(t, f.repo, "leaf", "1.1.0")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			if err := install.Upgrade(f.opts, "", false); err != nil {
				t.Fatal(err)
			}
			got := loadManifest(t, f.path)
			a, leaf := got.Skills["a"], got.Skills["leaf"]
			if a.Resolved.Version != "1.0.0" || leaf.Resolved.Version != "1.1.0" || !slices.Equal(leaf.Via, []string{"a"}) {
				t.Fatalf("a=%s leaf=%s via=%v", a.Resolved.Version, leaf.Resolved.Version, leaf.Via)
			}
			if missing {
				assertMissing(t, skillPath(f.root, "a"))
			} else if text := readText(t, filepath.Join(skillPath(f.root, "a"), "SKILL.md")); text != "local edit\n" {
				t.Fatalf("unmoved parent was rewritten: %q", text)
			}
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestNeededLegacyParentAccessFailureLeavesStateUntouched(t *testing.T) {
	for _, operation := range []string{"install", "upgrade"} {
		t.Run(operation, func(t *testing.T) {
			f := dependencyScope(t, false)
			leafRepo := testutil.NewRepo(t)
			dependencyRelease(t, leafRepo, "leaf", "1.0.0")
			m := loadManifest(t, f.path)
			m.Sources["other"] = testutil.URL(leafRepo)
			f.save(t, m)
			dependencyRelease(t, f.repo, "a", "1.0.0", "other/leaf@^1")
			f.add(t, "demo/a")
			m = loadManifest(t, f.path)
			pin := m.Skills["a"].Resolved
			pin.Requires = nil
			f.save(t, m)
			if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
				t.Fatal(err)
			}
			dependencyRelease(t, leafRepo, "leaf", "1.1.0")
			removeSourceAndCache(t, f.repo)
			before := f.snapshot(t)
			var err error
			if operation == "install" {
				err = install.Add(f.opts, "leaf@^1")
			} else {
				err = install.Upgrade(f.opts, "leaf", false)
			}
			assertUntouchedError(t, err, "read requirements demo/a")
			var access *dependency.AccessError
			if !errors.As(err, &access) || access.Source != "demo" || access.Name != "a" || access.Commit != pin.Commit || access.Operation != "read requirements" || !strings.Contains(err.Error(), pin.Commit) {
				t.Fatalf("missing parent access context: %v (%+v)", err, access)
			}
			assertSameTree(t, before, f.snapshot(t))
			assertNoTempDirs(t, f.root)
		})
	}
}

func TestLegacyParentAllowsCompatibleDependencyUpgrade(t *testing.T) {
	for _, major := range []bool{false, true} {
		t.Run(fmt.Sprintf("major=%t", major), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "a", "1.0.0", "leaf@^1")
			f.add(t, "a")
			m := loadManifest(t, f.path)
			m.Skills["a"].Resolved.Requires = nil
			f.save(t, m)
			if err := os.RemoveAll(skillPath(f.root, "a")); err != nil {
				t.Fatal(err)
			}
			dependencyRelease(t, f.repo, "leaf", "1.1.0")
			dependencyRelease(t, f.repo, "leaf", "2.0.0")
			f.upgrade(t, "leaf", major)
			got := loadManifest(t, f.path)
			if got.Skills["leaf"].Resolved.Version != "1.1.0" || !slices.Equal(got.Skills["leaf"].Via, []string{"a"}) {
				t.Fatalf("leaf = %+v", got.Skills["leaf"])
			}
			assertMissing(t, skillPath(f.root, "a"))
		})
	}
}
