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
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func diagnosticFixture(t *testing.T, global bool) dependencyFixture {
	t.Helper()
	f := dependencyScope(t, global)
	if !global {
		testutil.WriteFile(t, filepath.Join(f.root, "AGENTS.md"), healthyAgents)
	}
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
	f.add(t, "root")
	n, out := fixtureDoctor(t, f)
	if n != 0 || out != "ok\n" {
		t.Fatalf("fixture is unhealthy: %d\n%s", n, out)
	}
	return f
}

func fixtureDoctor(t *testing.T, f dependencyFixture) (int, string) {
	t.Helper()
	var out bytes.Buffer
	opts := f.opts
	opts.Out = &out
	n, err := install.Doctor(opts)
	if err != nil {
		t.Fatal(err)
	}
	return n, out.String()
}

func TestDoctorDependencyFindings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, dependencyFixture, *manifest.Manifest)
		want   string
	}{
		{
			name: "missing entry",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				delete(m.Skills, "leaf")
			},
			want: `missing dependency entry: root requires "demo/leaf@^1" (restore skills.leaf in the manifest)`,
		},
		{
			name: "missing directory",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				if err := os.RemoveAll(skillPath(f.root, "leaf")); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing dependency files: demo/leaf (required by root; run kitt install)",
		},
		{
			name: "missing skill file",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				if err := os.Remove(filepath.Join(skillPath(f.root, "leaf"), "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing dependency files: demo/leaf (required by root; run kitt install)",
		},
		{
			name: "incompatible pin",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Skills["leaf"].Resolved.Version = "2.0.0"
			},
			want: `incompatible pin: demo/leaf@2.0.0 (root requires "demo/leaf@^1"; run kitt install or upgrade the dependent)`,
		},
		{
			name: "branch tip cannot satisfy range",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Skills["leaf"].Resolved.Version = ""
			},
			want: "incompatible pin: demo/leaf@",
		},
		{
			name: "wrong source even with identical URL",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Sources["other"] = m.Sources["demo"]
				m.Skills["leaf"].Source = "other"
			},
			want: `wrong source: root requires "demo/leaf@^1" (skills.leaf uses "other")`,
		},
		{
			name: "unknown dependency alias",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				raw := []string{"other/leaf@^1"}
				m.Skills["root"].Resolved.Requires = &raw
			},
			want: `wrong source: root requires "other/leaf@^1" (unknown source alias "other" in manifest)`,
		},
		{
			name: "cycle",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				raw := []string{"demo/root@^1"}
				m.Skills["leaf"].Resolved.Requires = &raw
			},
			want: "dependency cycle: demo/leaf@1.0.0 -> demo/root@1.0.0 -> demo/leaf@1.0.0",
		},
		{
			name: "self cycle",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				raw := []string{"demo/leaf@^1"}
				m.Skills["leaf"].Resolved.Requires = &raw
			},
			want: "dependency cycle: demo/leaf@1.0.0 -> demo/leaf@1.0.0",
		},
		{
			name: "stale via",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Skills["leaf"].Via = []string{"ghost"}
			},
			want: "stale via: leaf (got [ghost]; want [root]; run kitt install)",
		},
		{
			name: "duplicate via",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Skills["leaf"].Via = []string{"root", "root"}
			},
			want: "stale via: leaf (got [root root]; want [root]; run kitt install)",
		},
		{
			name: "snapshot differs",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				raw := []string{}
				m.Skills["root"].Resolved.Requires = &raw
			},
			want: "dependency snapshot disagrees: root (resolved.requires differs from pinned SKILL.md; run kitt install)",
		},
		{
			name: "malformed reference alongside valid edge",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				raw := []string{"demo/leaf@^1", "../bad@^1"}
				m.Skills["root"].Resolved.Requires = &raw
			},
			want: `invalid dependencies: root resolved.requires (requires[1]: invalid dependency reference "../bad@^1"`,
		},
		{
			name: "explicit transitive constraint excludes pin",
			change: func(t *testing.T, f dependencyFixture, m *manifest.Manifest) {
				m.Skills["leaf"].Version = "2.0.0"
			},
			want: `incompatible pin: leaf@1.0.0 (manifest requests "2.0.0"; clear its resolved block and run kitt install)`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := diagnosticFixture(t, false)
			m := loadManifest(t, f.path)
			tc.change(t, f, m)
			f.save(t, m)
			before := f.snapshot(t)
			n, out := fixtureDoctor(t, f)
			if n == 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("findings=%d, want %q:\n%s", n, tc.want, out)
			}
			if !strings.HasSuffix(out, fmt.Sprintf("%d problem(s)\n", n)) || strings.Count(out, "\n") != n+1 {
				t.Fatalf("finding format/count: %d\n%s", n, out)
			}
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}

func TestDoctorDetachedDependencySubtree(t *testing.T) {
	f := diagnosticFixture(t, false)
	dependencyRelease(t, f.repo, "abandoned", "1.0.0")
	dependencyRelease(t, f.repo, "orphan", "1.0.0", "abandoned@^1")
	f.add(t, "orphan")
	m := loadManifest(t, f.path)
	direct := false
	m.Skills["orphan"].Direct = &direct
	f.save(t, m)
	const want = "orphaned dependency: abandoned (unreachable from any direct skill; promote it or remove its manifest entry)\n" +
		"orphaned dependency: orphan (unreachable from any direct skill; promote it or remove its manifest entry)\n" +
		"2 problem(s)\n"
	if n, out := fixtureDoctor(t, f); n != 2 || out != want {
		t.Fatalf("orphans: %d\n%s\nwant:\n%s", n, out, want)
	}
}

func TestDoctorModifiedMetadataNeverAuthoritative(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := diagnosticFixture(t, false)
			m := loadManifest(t, f.path)
			// Untrusted metadata tries to erase the real edge and introduce a cycle
			// and a missing skill. The saved snapshot must remain authoritative.
			testutil.WriteFile(t, filepath.Join(skillPath(f.root, "root"), "SKILL.md"),
				"---\nname: root\ndescription: modified\nmetadata:\n  requires: [root@^1, ghost@^1]\n---\n")
			if legacy {
				m.Skills["root"].Resolved.Requires = nil
				f.save(t, m)
			}
			_, out := fixtureDoctor(t, f)
			if !strings.Contains(out, "modified: root") || strings.Contains(out, "ghost") ||
				strings.Contains(out, "dependency cycle") || strings.Contains(out, "snapshot disagrees") || strings.Contains(out, "stale via") {
				t.Fatalf("trusted modified metadata:\n%s", out)
			}
			if strings.Contains(out, "dependency validation incomplete: root") != legacy {
				t.Fatalf("legacy incompleteness=%t:\n%s", legacy, out)
			}
			if strings.Contains(out, "orphaned dependency") {
				t.Fatalf("reported an orphan without authoritative evidence:\n%s", out)
			}
		})
	}
}

func TestDoctorLegacyRequirementsAndNormalizedSnapshots(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			f := diagnosticFixture(t, false)
			m := loadManifest(t, f.path)
			if legacy {
				for _, s := range m.Skills {
					s.Resolved.Requires = nil
				}
			} else {
				raw := []string{"leaf@^1", "demo/leaf@^1"}
				m.Skills["root"].Resolved.Requires = &raw
			}
			f.save(t, m)
			if n, out := fixtureDoctor(t, f); n != 0 || out != "ok\n" {
				t.Fatalf("legacy/normalized metadata: %d\n%s", n, out)
			}
		})
	}
}

func TestDoctorLegacyUnavailableIsIncomplete(t *testing.T) {
	f := diagnosticFixture(t, false)
	m := loadManifest(t, f.path)
	m.Skills["root"].Resolved.Requires = nil
	f.save(t, m)
	if err := os.RemoveAll(skillPath(f.root, "root")); err != nil {
		t.Fatal(err)
	}
	removeSourceAndCache(t, f.repo)
	t.Setenv("PATH", t.TempDir())
	_, out := fixtureDoctor(t, f)
	if !strings.Contains(out, "dependency validation incomplete: root (legacy pin has no requires snapshot and verified installed content is unavailable; run kitt install)") {
		t.Fatalf("legacy availability:\n%s", out)
	}
}

func TestDoctorNoDependencyKeysMatchesMain(t *testing.T) {
	for _, state := range []string{"healthy", "missing", "modified"} {
		t.Run(state, func(t *testing.T) {
			project := healthyProject(t)
			path := filepath.Join(project, manifest.FileName)
			m := loadManifest(t, path)
			s := m.Skills["review"]
			s.Direct, s.Via, s.Resolved.Requires = nil, nil, nil
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			wantCount, want := 0, "ok\n"
			switch state {
			case "missing":
				if err := os.RemoveAll(skillPath(project, "review")); err != nil {
					t.Fatal(err)
				}
				wantCount, want = 1, "missing: review (run kitt install)\n1 problem(s)\n"
			case "modified":
				testutil.WriteFile(t, filepath.Join(skillPath(project, "review"), "LOCAL.md"), "local\n")
				wantCount, want = 1, "modified: review (local changes; kitt install restores the pinned version)\n1 problem(s)\n"
			}
			if n, out := mustDoctor(t, project); n != wantCount || out != want {
				t.Fatalf("doctor = %d, %q; want main's %d, %q", n, out, wantCount, want)
			}
		})
	}
}

func TestDoctorOfflineReadOnlyIncludesHomeAndOwnershipMigration(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint(global), func(t *testing.T) {
			for _, finding := range []bool{false, true} {
				t.Run(fmt.Sprint(finding), func(t *testing.T) {
					f := diagnosticFixture(t, global)
					// Missing ownership state must not trigger readInstalled's migration.
					if err := os.Remove(filepath.Join(skillPath(f.root, "leaf"), "..", ".kitt-installed")); err != nil {
						t.Fatal(err)
					}
					if finding {
						m := loadManifest(t, f.path)
						m.Skills["leaf"].Via = nil
						f.save(t, m)
					}
					removeSourceAndCache(t, f.repo)
					// No Git executable is available; any source-cache initialization
					// would also be caught by the complete home snapshot below.
					t.Setenv("PATH", t.TempDir())
					home, err := os.UserHomeDir()
					if err != nil {
						t.Fatal(err)
					}
					beforeHome, beforeProject := snapshot(t, home), snapshot(t, f.opts.Root)
					n, out := fixtureDoctor(t, f)
					if finding && (n != 1 || !strings.Contains(out, "stale via: leaf")) || !finding && (n != 0 || out != "ok\n") {
						t.Fatalf("offline findings=%d:\n%s", n, out)
					}
					assertSameTree(t, beforeHome, snapshot(t, home))
					assertSameTree(t, beforeProject, snapshot(t, f.opts.Root))
				})
			}
		})
	}
}

func TestDoctorInvalidPinnedFrontmatterIsFinding(t *testing.T) {
	f := diagnosticFixture(t, false)
	m := loadManifest(t, f.path)
	testutil.WriteFile(t, filepath.Join(skillPath(f.root, "leaf"), "SKILL.md"), "not frontmatter\n")
	hash, err := skill.Hash(skillPath(f.root, "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	m.Skills["leaf"].Resolved.Hash = hash
	f.save(t, m)
	if n, out := fixtureDoctor(t, f); n != 1 || !strings.Contains(out, "invalid dependencies: leaf") {
		t.Fatalf("invalid pinned metadata: %d\n%s", n, out)
	}
}

func TestDoctorForeignRequiresMatchesDependencyFree(t *testing.T) {
	for _, tc := range []struct{ name, requires string }{
		{"scalar", `"python3"`},
		{"foreign_words", `"numpy pandas"`},
		{"foreign_list", `["numpy"]`},
	} {
		requires := tc.requires
		t.Run(tc.name, func(t *testing.T) {
			project := healthyProject(t)
			path := filepath.Join(project, manifest.FileName)
			m := loadManifest(t, path)
			s := m.Skills["review"]
			s.Direct, s.Via, s.Resolved.Requires = nil, nil, nil
			body := "---\nname: review\ndescription: Test skill review.\nmetadata:\n  requires: " + requires + "\n---\nreview 1.4.0\n"
			testutil.WriteFile(t, filepath.Join(skillPath(project, "review"), "SKILL.md"), body)
			hash, err := skill.Hash(skillPath(project, "review"))
			if err != nil {
				t.Fatal(err)
			}
			s.Resolved.Hash = hash
			if err := m.Save(path); err != nil {
				t.Fatal(err)
			}
			// Problem count 0 is the doctor exit code 0.
			if n, out := mustDoctor(t, project); n != 0 || out != "ok\n" {
				t.Fatalf("doctor = %d, %q; want main's 0, \"ok\\n\"", n, out)
			}
		})
	}
}

func TestDoctorLegacyFrontmatterDoesNotGovernVia(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			f := diagnosticFixture(t, false)
			f.add(t, "leaf") // Keep the child reachable even in the stale-edge case.
			m := loadManifest(t, f.path)
			m.Skills["root"].Resolved.Requires = nil
			if stale {
				testutil.WriteFile(t, filepath.Join(skillPath(f.root, "root"), "SKILL.md"),
					"---\nname: root\ndescription: Legacy root.\nmetadata:\n  version: 1.0.0\n---\n")
				hash, err := skill.Hash(skillPath(f.root, "root"))
				if err != nil {
					t.Fatal(err)
				}
				m.Skills["root"].Resolved.Hash = hash
			} else {
				m.Skills["leaf"].Via = nil
			}
			f.save(t, m)
			before := f.snapshot(t)
			if n, out := fixtureDoctor(t, f); n != 0 || out != "ok\n" {
				t.Fatalf("legacy metadata generated a via finding: %d\n%s", n, out)
			}
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}

func TestDoctorForeignRequiresWithDependenciesInPlay(t *testing.T) {
	for _, tc := range []struct{ name, requires string }{
		{"scalar", `"python3"`},
		{"foreign_words", `"numpy pandas"`},
		{"foreign_list", `["numpy"]`},
		{"mapping", `{runtime: python3}`},
		{"non_string_list", `[123]`},
	} {
		requires := tc.requires
		for _, snapshot := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/snapshot=%t", tc.name, snapshot), func(t *testing.T) {
				f := diagnosticFixture(t, false)
				dependencyRelease(t, f.repo, "b", "1.0.0")
				f.add(t, "b")
				path := filepath.Join(skillPath(f.root, "b"), "SKILL.md")
				testutil.WriteFile(t, path, "---\nname: b\ndescription: Foreign dependencies.\nmetadata:\n  requires: "+requires+"\n---\n")
				m := loadManifest(t, f.path)
				s := m.Skills["b"]
				var err error
				s.Resolved.Hash, err = skill.Hash(skillPath(f.root, "b"))
				if err != nil {
					t.Fatal(err)
				}
				if !snapshot {
					s.Resolved.Requires = nil
				}
				f.save(t, m)
				removeSourceAndCache(t, f.repo)
				before := f.snapshot(t)
				if n, out := fixtureDoctor(t, f); n != 0 || out != "ok\n" {
					t.Fatalf("doctor = %d, %q; want no findings", n, out)
				}
				assertSameTree(t, before, f.snapshot(t))
			})
		}
	}
}
