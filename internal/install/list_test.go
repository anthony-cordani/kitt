package install_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
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

func TestListNoDependencyEdgesMatchesMain(t *testing.T) {
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
				if got := mustList(t, project); got != want {
					t.Fatalf("list = %q, want main's %q", got, want)
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
