package install_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func installedTree(t *testing.T, f dependencyFixture) string {
	t.Helper()
	var out bytes.Buffer
	opts := f.opts
	opts.Out = &out
	if err := install.List(opts); err != nil {
		t.Fatal(err)
	}
	tree, _, _ := strings.Cut(out.String(), "Available in ")
	return tree
}

func TestListDependencyDiamondAndOrphanTree(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			f := dependencyScope(t, global)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "left", "1.0.0", "leaf@^1")
			dependencyRelease(t, f.repo, "right", "1.0.0", "leaf@^1.0")
			// Reverse declaration order must not affect the printed tree.
			dependencyRelease(t, f.repo, "root", "1.0.0", "right@^1", "left@^1")
			dependencyRelease(t, f.repo, "abandoned", "1.0.0")
			dependencyRelease(t, f.repo, "orphan", "1.0.0", "abandoned@^1")
			f.add(t, "root@^1")
			f.add(t, "orphan")
			m := loadManifest(t, f.path)
			direct := false
			m.Skills["orphan"].Direct = &direct
			f.save(t, m)
			const want = "Installed:\n" +
				"  demo/root@1.0.0 (requested ^1)\n" +
				"  |-- demo/left@1.0.0 (requires ^1)\n" +
				"  |   `-- demo/leaf@1.0.0 (requires ^1)\n" +
				"  `-- demo/right@1.0.0 (requires ^1)\n" +
				"      `-- demo/leaf@1.0.0 (requires ^1.0) [already shown]\n" +
				"Orphaned transitive skills:\n" +
				"  demo/orphan@1.0.0 (requested ^1.0.0)\n" +
				"  `-- demo/abandoned@1.0.0 (requires ^1)\n"
			for range 3 {
				if got := installedTree(t, f); got != want {
					t.Fatalf("list tree:\n%s\nwant:\n%s", got, want)
				}
			}
		})
	}
}

func TestListDirectDependency(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	f.add(t, "root")
	f.add(t, "leaf")
	const want = "Installed:\n" +
		"  demo/leaf@1.0.0 [direct]\n" +
		"  demo/root@1.0.0 (requested ^1.0.0)\n" +
		"  `-- demo/leaf@1.0.0 (requires ^1) [direct] [already shown]\n"
	if got := installedTree(t, f); got != want {
		t.Fatalf("got:\n%swant:\n%s", got, want)
	}
}

func TestListLegacyFormatting(t *testing.T) {
	project := healthyProject(t)
	path := filepath.Join(project, "kitt.toml")
	m := loadManifest(t, path)
	s := m.Skills["review"]
	s.Direct, s.Via, s.Resolved.Requires = nil, nil, nil
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	const want = "Installed:\n  review  1.4.0  demo\nAvailable in demo:\n  review  1.4.0  installed\n"
	if got := mustList(t, project); got != want {
		t.Fatalf("legacy list = %q, want %q", got, want)
	}
}

func TestListNoDependencyEdges(t *testing.T) {
	for _, keys := range []string{"absent", "empty snapshots", "transitive flag only"} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing=%v", keys, missing), func(t *testing.T) {
				project := healthyProject(t)
				path := filepath.Join(project, "kitt.toml")
				m := loadManifest(t, path)
				s := m.Skills["review"]
				s.Direct, s.Via, s.Resolved.Requires = nil, nil, nil
				if keys != "absent" {
					direct, requires := keys != "transitive flag only", []string{}
					s.Direct, s.Via, s.Resolved.Requires = &direct, []string{}, &requires
				}
				if err := m.Save(path); err != nil {
					t.Fatal(err)
				}
				// Golden strings from main's flat table, including available rows.
				want := "Installed:\n  review  1.4.0  demo\nAvailable in demo:\n  review  1.4.0  installed\n"
				if missing {
					if err := os.RemoveAll(skillPath(project, "review")); err != nil {
						t.Fatal(err)
					}
					want = "Installed:\n  review  not installed  demo\nAvailable in demo:\n  review  1.4.0  installed\n"
				}
				if keys == "transitive flag only" {
					label := "1.4.0"
					if missing {
						label += " [not installed]"
					}
					want = "Installed:\nOrphaned transitive skills:\n  demo/review@" + label + " (requested ^1.4.0)\nAvailable in demo:\n  review  1.4.0  installed\n"
				}
				if got := mustList(t, project); got != want {
					t.Fatalf("list = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestListLegacyFrontmatterDependencyUsesTree(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	f.add(t, "root")
	m := loadManifest(t, f.path)
	for _, s := range m.Skills {
		s.Direct, s.Via, s.Resolved.Requires = nil, nil, nil
	}
	f.save(t, m)
	const want = "Installed:\n" +
		"  demo/leaf@1.0.0 [direct]\n" +
		"  demo/root@1.0.0 (requested ^1.0.0)\n" +
		"  `-- demo/leaf@1.0.0 (requires ^1) [direct] [already shown]\n"
	if got := installedTree(t, f); got != want {
		t.Fatalf("legacy dependency tree:\n%swant:\n%s", got, want)
	}
}

func TestListMalformedGraphTerminates(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	f.add(t, "root")
	m := loadManifest(t, f.path)
	raw := []string{"demo/root@^1", "../../bad@^1", "demo/missing@^1"}
	m.Skills["leaf"].Resolved.Requires = &raw
	f.save(t, m)
	const want = "Installed:\n" +
		"  demo/root@1.0.0 (requested ^1.0.0) [direct]\n" +
		"  `-- demo/leaf@1.0.0 (requires ^1)\n" +
		"      |-- demo/missing@^1 (missing entry)\n" +
		"      `-- demo/root@1.0.0 (requires ^1) [direct] [already shown]\n"
	if got := installedTree(t, f); got != want {
		t.Fatalf("got:\n%swant:\n%s", got, want)
	}
}

func TestListMissingDependencyKeepsSelectedVersionAndAvailableTolerance(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	f.add(t, "root")
	if err := os.RemoveAll(skillPath(f.root, "leaf")); err != nil {
		t.Fatal(err)
	}
	m := loadManifest(t, f.path)
	m.Sources["gone"] = testutil.URL(filepath.Join(f.root, "no-such-repo"))
	f.save(t, m)
	out := mustList(t, f.root)
	if !strings.Contains(out, "demo/leaf@1.0.0 [not installed] (requires ^1)") ||
		!strings.Contains(out, "Available in demo:") || !strings.Contains(out, "Available in gone: unreachable") {
		t.Fatalf("list missing pin/source:\n%s", out)
	}
}

func TestListAndDoctorLegacyUnavailableDependenciesAgree(t *testing.T) {
	for _, state := range []string{"missing", "modified"} {
		t.Run(state, func(t *testing.T) {
			f := diagnosticFixture(t, false)
			m := loadManifest(t, f.path)
			m.Skills["root"].Resolved.Requires = nil
			f.save(t, m)
			if state == "missing" {
				if err := os.RemoveAll(skillPath(f.root, "root")); err != nil {
					t.Fatal(err)
				}
			} else {
				testutil.WriteFile(t, filepath.Join(skillPath(f.root, "root"), "SKILL.md"), "modified metadata\n")
			}
			list := installedTree(t, f)
			_, doctor := fixtureDoctor(t, f)
			if !strings.Contains(list, "Dependency information incomplete") || !strings.Contains(doctor, "dependency validation incomplete: root") {
				t.Fatalf("commands must report incomplete information:\nlist:\n%s\ndoctor:\n%s", list, doctor)
			}
			if strings.Contains(list, "Orphaned transitive skills") || strings.Contains(doctor, "orphaned dependency") {
				t.Fatalf("commands claimed an orphan without evidence:\nlist:\n%s\ndoctor:\n%s", list, doctor)
			}
			if !strings.Contains(list, "demo/leaf@1.0.0") {
				t.Fatalf("list omitted the child with unknown reachability:\n%s", list)
			}
		})
	}
}

func TestListIncompleteHeadingOnlyForTransitiveEntries(t *testing.T) {
	t.Run("unresolved direct", func(t *testing.T) {
		f := dependencyScope(t, false)
		dependencyRelease(t, f.repo, "leaf", "1.0.0")
		dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
		f.add(t, "root")
		m := loadManifest(t, f.path)
		m.Skills["new"] = &manifest.Skill{Source: "demo"}
		f.save(t, m)
		got := installedTree(t, f)
		if strings.Contains(got, "Dependency information incomplete") || strings.Contains(got, "Orphaned transitive skills") {
			t.Fatalf("unresolved entry changed the heading:\n%s", got)
		}
		if !strings.Contains(got, "demo/root@1.0.0") {
			t.Fatalf("root missing:\n%s", got)
		}
	})
	t.Run("real orphan", func(t *testing.T) {
		f := dependencyScope(t, false)
		dependencyRelease(t, f.repo, "leaf", "1.0.0")
		dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
		dependencyRelease(t, f.repo, "abandoned", "1.0.0")
		f.add(t, "root")
		f.add(t, "abandoned")
		m := loadManifest(t, f.path)
		direct := false
		m.Skills["abandoned"].Direct = &direct
		m.Skills["new"] = &manifest.Skill{Source: "demo"}
		f.save(t, m)
		got := installedTree(t, f)
		if !strings.Contains(got, "Orphaned transitive skills") || !strings.Contains(got, "demo/abandoned@1.0.0") {
			t.Fatalf("orphan section:\n%s", got)
		}
		if strings.Contains(got, "Dependency information incomplete") {
			t.Fatalf("unresolved entry flipped the heading:\n%s", got)
		}
	})
}

func updatesBlock(out string) (string, bool) {
	_, after, ok := strings.Cut(out, "Updates:\n")
	if !ok {
		return "", false
	}
	return after, true
}

func assertUpdates(t *testing.T, out, want string) {
	t.Helper()
	got, ok := updatesBlock(out)
	if !ok || got != want {
		t.Fatalf("updates = %q, want %q\n%s", got, want, out)
	}
}

func assertNoUpdates(t *testing.T, out string) {
	t.Helper()
	if _, ok := updatesBlock(out); ok {
		t.Fatalf("unexpected Updates:\n%s", out)
	}
}

func TestListUpdatesMajorOnly(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	out := mustList(t, project)
	assertUpdates(t, out, "  review  1.4.0 -> 2.0.0  (major: kitt upgrade --major)\n")
}

func TestListUpdatesInRange(t *testing.T) {
	project, skills := newPair(t)
	release(t, skills, "review", "1.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "review@^1")
	release(t, skills, "review", "1.1.0")
	out := mustList(t, project)
	assertUpdates(t, out, "  review  1.0.0 -> 1.1.0\n")
}

func TestListUpdatesInRangeAndMajor(t *testing.T) {
	project, skills := newPair(t)
	release(t, skills, "review", "1.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "review@^1")
	release(t, skills, "review", "1.1.0")
	release(t, skills, "review", "2.0.0")
	out := mustList(t, project)
	assertUpdates(t, out, "  review  1.0.0 -> 1.1.0  (2.0.0 with kitt upgrade --major)\n")
}

func TestListUpdatesUpToDate(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review")
	if got := skillEntry(t, project, "review").Resolved.Version; got != "2.0.0" {
		t.Fatalf("resolved version = %s, want 2.0.0", got)
	}
	assertNoUpdates(t, mustList(t, project))
}

func TestListUpdatesExactPin(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@1.0.0")
	sk := skillEntry(t, project, "review")
	if sk.Version != "1.0.0" || sk.Resolved == nil || sk.Resolved.Version != "1.0.0" {
		t.Fatalf("pin = %+v", sk)
	}
	out := mustList(t, project)
	if !lineWith(out, "review", "2.0.0", "installed") {
		t.Fatalf("available latest missing:\n%s", out)
	}
	assertNoUpdates(t, out)
}

func TestListUpdatesInvalidConstraint(t *testing.T) {
	project, skills := newPair(t)
	release(t, skills, "review", "1.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "review@^1")
	release(t, skills, "review", "1.1.0")
	path := filepath.Join(project, manifest.FileName)
	m := loadManifest(t, path)
	m.Skills["review"].Version = "1.0"
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	assertNoUpdates(t, mustList(t, project))
}

func TestListUpdatesSkipsDependencies(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "held", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1", "held@^1")
	f.add(t, "root")
	f.add(t, "held")
	dependencyRelease(t, f.repo, "leaf", "1.1.0")
	dependencyRelease(t, f.repo, "held", "1.1.0")
	m := loadManifest(t, f.path)
	if m.Skills["leaf"].IsDirect() || !m.Skills["held"].IsDirect() {
		t.Fatalf("leaf direct = %t, held direct = %t", m.Skills["leaf"].IsDirect(), m.Skills["held"].IsDirect())
	}
	out := mustList(t, f.root)
	if !lineWith(out, "leaf", "1.1.0") || !lineWith(out, "held", "1.1.0") {
		t.Fatalf("newer releases missing from available:\n%s", out)
	}
	assertNoUpdates(t, out)
}

func TestListUpdatesUnreachableSource(t *testing.T) {
	project, skills := newPair(t)
	release(t, skills, "review", "1.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "review@^1")
	release(t, skills, "review", "1.1.0")
	// Fill the cache with 1.1.0, so only the unreachable source keeps it out of Updates.
	assertUpdates(t, mustList(t, project), "  review  1.0.0 -> 1.1.0\n")
	moved := filepath.Join(filepath.Dir(skills), "demo-moved")
	if err := os.Rename(skills, moved); err != nil {
		t.Fatal(err)
	}
	out := mustList(t, project)
	if !strings.Contains(out, "Available in demo: unreachable") {
		t.Fatalf("missing unreachable line:\n%s", out)
	}
	assertNoUpdates(t, out)
}

func TestListUpdatesEmptyConstraint(t *testing.T) {
	t.Run("in range", func(t *testing.T) {
		project, skills := newPair(t)
		release(t, skills, "review", "1.0.0")
		writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
		mustAdd(t, project, "review@^1")
		clearVersion(t, project, "review")
		release(t, skills, "review", "1.1.0")
		out := mustList(t, project)
		assertUpdates(t, out, "  review  1.0.0 -> 1.1.0\n")
	})
	t.Run("major", func(t *testing.T) {
		project, skills := newPair(t)
		release(t, skills, "review", "1.0.0")
		writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
		mustAdd(t, project, "review@^1")
		clearVersion(t, project, "review")
		release(t, skills, "review", "2.0.0")
		out := mustList(t, project)
		assertUpdates(t, out, "  review  1.0.0 -> 2.0.0  (major: kitt upgrade --major)\n")
	})
}

func clearVersion(t *testing.T, project, name string) {
	t.Helper()
	path := filepath.Join(project, manifest.FileName)
	m := loadManifest(t, path)
	m.Skills[name].Version = ""
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := loadManifest(t, path).Skills[name].Version; got != "" {
		t.Fatalf("version = %q, want empty", got)
	}
}

func TestListUpdatesUnreleasedPin(t *testing.T) {
	project, skills := newPair(t)
	testutil.WriteSkill(t, skills, "review", "", "review body")
	testutil.Commit(t, skills, "review")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review")
	sk := skillEntry(t, project, "review")
	if sk.Resolved == nil || sk.Resolved.Version != "" {
		t.Fatalf("resolved = %+v, want an unreleased pin", sk.Resolved)
	}
	release(t, skills, "review", "1.0.0")
	out := mustList(t, project)
	if !lineWith(out, "review", "1.0.0", "installed") {
		t.Fatalf("available line missing:\n%s", out)
	}
	assertNoUpdates(t, out)
}

func TestListUpdatesMatchUpgrade(t *testing.T) {
	t.Run("in range", func(t *testing.T) {
		project, skills := newPair(t)
		release(t, skills, "review", "1.0.0")
		writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
		mustAdd(t, project, "review@^1")
		release(t, skills, "review", "1.1.0")
		assertUpdates(t, mustList(t, project), "  review  1.0.0 -> 1.1.0\n")
		mustUpgrade(t, project, "", false)
		if got := skillEntry(t, project, "review").Resolved.Version; got != "1.1.0" {
			t.Fatalf("upgraded version = %s, want 1.1.0", got)
		}
		assertNoUpdates(t, mustList(t, project))
	})
	t.Run("major", func(t *testing.T) {
		project, _ := standard(t)
		mustAdd(t, project, "demo/review@^1")
		const want = "  review  1.4.0 -> 2.0.0  (major: kitt upgrade --major)\n"
		assertUpdates(t, mustList(t, project), want)
		mustUpgrade(t, project, "", false)
		if got := skillEntry(t, project, "review").Resolved.Version; got != "1.4.0" {
			t.Fatalf("resolved version = %s, want 1.4.0", got)
		}
		assertUpdates(t, mustList(t, project), want)
		mustUpgrade(t, project, "", true)
		if got := skillEntry(t, project, "review").Resolved.Version; got != "2.0.0" {
			t.Fatalf("upgraded version = %s, want 2.0.0", got)
		}
		assertNoUpdates(t, mustList(t, project))
	})
}
