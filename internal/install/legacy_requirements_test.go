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
	for _, requires := range []string{`"python3"`, `["numpy"]`} {
		t.Run(requires, func(t *testing.T) {
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
	for _, requires := range []string{`"python3"`, `["numpy"]`} {
		for _, target := range []string{"legacy", "parent"} {
			t.Run(requires+"/"+target, func(t *testing.T) {
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
	for _, requires := range []string{`[]`, `["leaf@^1", 123]`, `"python3"`, `["numpy"]`, `{runtime: python3}`} {
		for _, mode := range []string{"restore", "upgrade", "install"} {
			t.Run(requires+"/"+mode, func(t *testing.T) {
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
