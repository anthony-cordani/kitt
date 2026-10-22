package install_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestRequirementFormsInstallRestoreListDoctorUpgrade(t *testing.T) {
	for _, form := range []string{"string", "sequence"} {
		t.Run(form, func(t *testing.T) {
			f := dependencyScope(t, false)
			testutil.WriteFile(t, filepath.Join(f.root, "AGENTS.md"), healthyAgents)
			release := func(name, version string, refs ...string) {
				t.Helper()
				testutil.WriteSkillRequires(t, f.repo, name, version, refs)
				if form == "string" {
					body := fmt.Sprintf("---\nname: %s\ndescription: Test skill.\nmetadata:\n  version: %q\n  requires: %q\n---\n", name, version, strings.Join(refs, " "))
					testutil.WriteFile(t, filepath.Join(f.repo, "skills", name, "SKILL.md"), body)
				}
				testutil.Commit(t, f.repo, name+" "+version)
				testutil.Git(t, f.repo, "tag", name+"/v"+version)
			}
			release("leaf", "1.0.0")
			release("code-review", "1.0.0", "leaf@^1")
			release("us-review", "1.0.0")
			release("us-deliver", "1.0.0", "code-review@^1.0.0", "demo/us-review@1.0.0")
			f.add(t, "us-deliver")
			m := loadManifest(t, f.path)
			if len(m.Skills) != 4 {
				t.Fatalf("closure = %v", m.Skills)
			}
			for name, want := range map[string][]string{
				"us-deliver":  {"demo/code-review@^1.0.0", "demo/us-review@1.0.0"},
				"code-review": {"demo/leaf@^1"},
				"us-review":   {},
				"leaf":        {},
			} {
				s := m.Skills[name]
				if s == nil || s.Resolved == nil || s.Resolved.Requires == nil || !reflect.DeepEqual(*s.Resolved.Requires, want) {
					t.Fatalf("%s snapshot = %+v, want %v", name, s, want)
				}
				if s.IsDirect() != (name == "us-deliver") {
					t.Fatalf("%s direct = %t", name, s.IsDirect())
				}
				if _, err := os.Stat(filepath.Join(skillPath(f.root, name), "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			}
			if raw := readText(t, f.path); strings.Count(raw, "requires = [") != 4 {
				t.Fatalf("snapshots must remain TOML arrays:\n%s", raw)
			}
			const wantTree = "Installed:\n" +
				"  demo/us-deliver@1.0.0 (requested ^1.0.0)\n" +
				"  |-- demo/code-review@1.0.0 (requires ^1.0.0)\n" +
				"  |   `-- demo/leaf@1.0.0 (requires ^1)\n" +
				"  `-- demo/us-review@1.0.0 (requires 1.0.0)\n"
			checkDiagnostics := func() {
				t.Helper()
				if tree := installedTree(t, f); tree != wantTree {
					t.Fatalf("list =\n%s\nwant:\n%s", tree, wantTree)
				}
				if n, out := fixtureDoctor(t, f); n != 0 || out != "ok\n" {
					t.Fatalf("doctor = %d, %q", n, out)
				}
			}
			checkDiagnostics()
			// Without a snapshot, list and doctor must read Meta.Requires.
			saved := m.Skills["us-deliver"].Resolved.Requires
			m.Skills["us-deliver"].Resolved.Requires = nil
			f.save(t, m)
			checkDiagnostics()
			m.Skills["us-deliver"].Resolved.Requires = saved
			f.save(t, m)
			before := f.snapshot(t)
			if err := os.RemoveAll(skillPath(f.root, "code-review")); err != nil {
				t.Fatal(err)
			}
			f.restore(t)
			assertSameTree(t, before, f.snapshot(t))
			release("us-deliver", "1.1.0", "code-review@^1.0.0", "demo/us-review@1.0.0")
			if err := install.Upgrade(f.opts, "us-deliver", false); err != nil {
				t.Fatal(err)
			}
			upgraded := loadManifest(t, f.path)
			if s := upgraded.Skills["us-deliver"]; s.Resolved.Version != "1.1.0" || !reflect.DeepEqual(*s.Resolved.Requires, *saved) {
				t.Fatalf("upgraded skill = %+v", s)
			}
			if n, out := fixtureDoctor(t, f); n != 0 || out != "ok\n" {
				t.Fatalf("doctor after upgrade = %d, %q", n, out)
			}
		})
	}
}

func TestInstallRejectsInvalidRequirementFormsWithoutFallback(t *testing.T) {
	for _, tc := range []struct{ name, requires, want string }{
		{"string", `"code-review@^1 db@@1"`, `metadata.requires entry 2 "db@@1" at line 6`},
		{"sequence", `["code-review@^1", "db@@1"]`, "metadata.requires[1]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "root", "1.0.0")
			body := "---\nname: root\ndescription: Test skill.\nmetadata:\n  version: 1.1.0\n  requires: " + tc.requires + "\n---\n"
			testutil.WriteFile(t, filepath.Join(f.repo, "skills", "root", "SKILL.md"), body)
			testutil.Commit(t, f.repo, "invalid requirements on newest release")
			testutil.Git(t, f.repo, "tag", "root/v1.1.0")
			before := f.snapshot(t)
			err := install.Add(f.opts, "root")
			assertUntouchedError(t, err, tc.want)
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}
