package install_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

// These are main's pre-dependency manifest types and encoder rules.
type mainResolved struct {
	Version string `toml:"version"`
	Commit  string `toml:"commit"`
	Hash    string `toml:"hash"`
}
type mainSkill struct {
	Source   string        `toml:"source"`
	Version  string        `toml:"version,omitempty"`
	Resolved *mainResolved `toml:"resolved,omitempty"`
}

func assertMainManifest(t *testing.T, path string) {
	t.Helper()
	m := loadManifest(t, path)
	old := struct {
		Sources map[string]string     `toml:"sources"`
		Skills  map[string]*mainSkill `toml:"skills"`
	}{Sources: m.Sources, Skills: map[string]*mainSkill{}}
	for name, s := range m.Skills {
		if s.Direct != nil || s.Via != nil || s.Resolved.Requires != nil {
			t.Fatalf("dependency-free %s has new keys: %+v", name, s)
		}
		old.Skills[name] = &mainSkill{Source: s.Source, Version: s.Version, Resolved: &mainResolved{s.Resolved.Version, s.Resolved.Commit, s.Resolved.Hash}}
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(old); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, path); got != b.String() {
		t.Fatalf("manifest differs from main:\n%s\nwant:\n%s", got, &b)
	}
}

func TestNoDependencyManifestMatchesMain(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, version := range []string{"1.0.0", ""} {
			t.Run(fmt.Sprintf("global=%t/version=%s", global, version), func(t *testing.T) {
				f := dependencyScope(t, global)
				if version == "" {
					testutil.WriteSkill(t, f.repo, "foo", "", "branch tip")
					testutil.Commit(t, f.repo, "foo")
				} else {
					dependencyRelease(t, f.repo, "foo", version)
				}
				// Restore unresolved entries, install, and upgrade must all use main's bytes.
				m := loadManifest(t, f.path)
				m.Skills["foo"] = &manifest.Skill{Source: "demo"}
				f.save(t, m)
				f.restore(t)
				assertMainManifest(t, f.path)
				f.add(t, "foo")
				assertMainManifest(t, f.path)
				if version != "" {
					dependencyRelease(t, f.repo, "foo", "1.1.0")
					f.upgrade(t, "", false)
					assertMainManifest(t, f.path)
				}
				// Comments prove an unchanged restore/upgrade did not save the file at all.
				text := "# keep formatting\n\n" + readText(t, f.path)
				testutil.WriteFile(t, f.path, text)
				f.restore(t)
				f.upgrade(t, "", false)
				if readText(t, f.path) != text {
					t.Fatal("unchanged manifest saved")
				}
			})
		}
	}
}

func TestDependencyKeysFollowFinalEdges(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	dependencyRelease(t, f.repo, "root", "1.0.0")
	f.add(t, "root")
	assertMainManifest(t, f.path)
	dependencyRelease(t, f.repo, "root", "1.1.0", "leaf@^1")
	f.upgrade(t, "root", false)
	m := loadManifest(t, f.path)
	for name, s := range m.Skills {
		if s.Resolved.Requires == nil {
			t.Fatalf("%s missing snapshot", name)
		}
	}
	if m.Skills["leaf"].Direct == nil || *m.Skills["leaf"].Direct || len(m.Skills["leaf"].Via) != 1 {
		t.Fatal("dependency graph missing")
	}
	dependencyRelease(t, f.repo, "root", "1.2.0")
	f.upgrade(t, "root", false)
	assertMainManifest(t, f.path)
	f.restore(t)
	assertMainManifest(t, f.path)
}

func TestLegacySourceAliasesRemainValid(t *testing.T) {
	for _, alias := range []string{"Team Skills", "~acme", "équipe"} {
		t.Run(alias, func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "foo", "1.0.0")
			m := loadManifest(t, f.path)
			m.Sources = map[string]string{alias: testutil.URL(f.repo), "unused bad alias": "file:///unused"}
			f.save(t, m)
			f.add(t, alias+"/foo")
			f.restore(t)
			dependencyRelease(t, f.repo, "foo", "1.1.0")
			f.upgrade(t, "foo", false)
			if loadManifest(t, f.path).Skills["foo"].Source != alias {
				t.Fatal("source changed")
			}
			assertMainManifest(t, f.path)
		})
	}
}

func TestInstallDirectWithoutRangeResolvesLatestAllowed(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprint(guarded), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "foo", "1.0.0")
			f.add(t, "foo@1.0.0")
			if guarded {
				dependencyRelease(t, f.repo, "root", "1.0.0", "foo@^1")
				f.add(t, "root")
			}
			dependencyRelease(t, f.repo, "foo", "1.4.0")
			dependencyRelease(t, f.repo, "foo", "2.0.0")
			f.add(t, "foo")
			want := "2.0.0"
			if guarded {
				want = "1.4.0"
			}
			assertResolved(t, loadManifest(t, f.path).Skills["foo"], "^"+want, want)
		})
	}
}

func TestInstallSourceSwitchChecksDependents(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(fmt.Sprint(guarded), func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "foo", "1.0.0")
			f.add(t, "foo")
			if guarded {
				dependencyRelease(t, f.repo, "root", "1.0.0", "foo@^1")
				f.add(t, "root")
			}
			other := testutil.NewRepo(t)
			dependencyRelease(t, other, "foo", "2.0.0")
			m := loadManifest(t, f.path)
			m.Sources["other"] = testutil.URL(other)
			f.save(t, m)
			before := f.snapshot(t)
			err := install.Add(f.opts, "other/foo")
			if guarded {
				assertUntouchedError(t, err, "source switch is blocked")
				if !strings.Contains(err.Error(), "demo/root@1.0.0") {
					t.Fatal(err)
				}
				assertSameTree(t, before, f.snapshot(t))
			} else {
				if err != nil {
					t.Fatal(err)
				}
				s := loadManifest(t, f.path).Skills["foo"]
				if s.Source != "other" || s.Resolved.Version != "2.0.0" {
					t.Fatalf("switch = %+v", s)
				}
			}
		})
	}
}

func TestPinnedInvalidConstraintOnlyFailsWhenResolving(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "foo", "1.0.0")
	f.add(t, "foo")
	m := loadManifest(t, f.path)
	m.Skills["foo"].Version = "~1.2"
	f.save(t, m)
	before := readText(t, f.path)
	f.restore(t)
	if readText(t, f.path) != before {
		t.Fatal("unchanged restore saved")
	}
	if err := install.Upgrade(f.opts, "foo", false); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("upgrade = %v", err)
	}
	m = loadManifest(t, f.path)
	m.Skills["foo"].Resolved = nil
	f.save(t, m)
	if err := install.Restore(f.opts); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("unresolved restore = %v", err)
	}
}

func TestInstallAndLegacyReadsAllowEmptyDescription(t *testing.T) {
	f := dependencyScope(t, false)
	path := filepath.Join(f.repo, "skills", "foo", "SKILL.md")
	testutil.WriteFile(t, path, "---\nname: foo\n---\nlegacy skill\n")
	testutil.Commit(t, f.repo, "empty description")
	testutil.Git(t, f.repo, "tag", "foo/v1.0.0")
	f.add(t, "foo")
	f.restore(t)
	if err := os.RemoveAll(skillPath(f.root, "foo")); err != nil {
		t.Fatal(err)
	}
	f.restore(t)
	assertMainManifest(t, f.path)
}

func TestUpgradeBulkTransitiveSmokeAndMajorNotes(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, bulk := range []bool{false, true} {
			t.Run(fmt.Sprintf("direct=%t/bulk=%t", direct, bulk), func(t *testing.T) {
				f := dependencyScope(t, false)
				dependencyRelease(t, f.repo, "code-review", "1.0.0")
				dependencyRelease(t, f.repo, "us-review", "1.0.0")
				dependencyRelease(t, f.repo, "us-deliver", "1.0.0", "code-review@^1.0.0", "us-review@^1.0.0")
				f.add(t, "us-deliver")
				if direct {
					f.add(t, "code-review")
				}
				dependencyRelease(t, f.repo, "code-review", "1.1.0")
				out := f.upgrade(t, "", false)
				s := loadManifest(t, f.path).Skills["code-review"]
				if s.Resolved.Version != "1.1.0" || !strings.Contains(out, "upgraded code-review") {
					t.Fatalf("bulk upgrade: %s, %+v", out, s)
				}
				dependencyRelease(t, f.repo, "code-review", "2.0.0")
				name := "code-review"
				if bulk {
					name = ""
				}
				// Named direct upgrades intentionally retain the existing refusal contract.
				if direct && !bulk {
					return
				}
				out = f.upgrade(t, name, true)
				want := "code-review 2.0.0 is available but demo/us-deliver@1.0.0 requires ^1.0.0"
				if !strings.Contains(out, want) {
					t.Fatalf("missing note %q:\n%s", want, out)
				}
				if loadManifest(t, f.path).Skills["code-review"].Resolved.Version != "1.1.0" {
					t.Fatal("blocked major installed")
				}
			})
		}
	}
}

func TestUpgradeMajorBulkFallsBackAndUpgradesOtherTargets(t *testing.T) {
	f := dependencyScope(t, false)
	dependencyRelease(t, f.repo, "leaf", "1.0.0")
	f.add(t, "leaf")
	dependencyRelease(t, f.repo, "parent", "1.0.0", "leaf@^1")
	f.add(t, "parent")
	dependencyRelease(t, f.repo, "other", "1.0.0")
	f.add(t, "other")
	dependencyRelease(t, f.repo, "leaf", "1.5.0")
	dependencyRelease(t, f.repo, "leaf", "2.0.0")
	dependencyRelease(t, f.repo, "other", "1.1.0")
	out := f.upgrade(t, "", true)
	m := loadManifest(t, f.path)
	assertResolved(t, m.Skills["leaf"], "^1.5.0", "1.5.0")
	assertResolved(t, m.Skills["other"], "^1.1.0", "1.1.0")
	if !strings.Contains(out, "leaf 2.0.0 is available but demo/parent@1.0.0 requires ^1") {
		t.Fatalf("missing note: %s", out)
	}
}

func TestRequirementsCanInheritLegacySourceAliases(t *testing.T) {
	for _, alias := range []string{"Team Skills", "~acme", "équipe"} {
		t.Run(alias, func(t *testing.T) {
			f := dependencyScope(t, false)
			dependencyRelease(t, f.repo, "leaf", "1.0.0")
			dependencyRelease(t, f.repo, "root", "1.0.0", "leaf@^1")
			m := loadManifest(t, f.path)
			m.Sources = map[string]string{alias: testutil.URL(f.repo)}
			f.save(t, m)
			f.add(t, "root")
			f.restore(t)
			dependencyRelease(t, f.repo, "leaf", "1.1.0")
			f.upgrade(t, "", false)
			if loadManifest(t, f.path).Skills["leaf"].Resolved.Version != "1.1.0" {
				t.Fatal("inherited alias did not resolve")
			}
			// An author writing the same alias in frontmatter still must obey the
			// whitelist. Inheritance does not broaden metadata.requires grammar.
			dependencyRelease(t, f.repo, "bad", "1.0.0", alias+"/leaf@^1")
			before := f.snapshot(t)
			assertUntouchedError(t, install.Add(f.opts, "bad"), "invalid dependency reference")
			assertSameTree(t, before, f.snapshot(t))
		})
	}
}

func TestBulkUpgradeReconsidersTransitiveBoundsFromNewParent(t *testing.T) {
	for _, dependencyFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(dependencyFirst), func(t *testing.T) {
			f := dependencyScope(t, false)
			leaf, parent := "zulu", "alpha"
			if dependencyFirst {
				leaf, parent = parent, leaf
			}
			dependencyRelease(t, f.repo, leaf, "1.0.0")
			dependencyRelease(t, f.repo, parent, "1.0.0", leaf+"@^1")
			dependencyRelease(t, f.repo, "root", "1.0.0", parent+"@^1")
			f.add(t, "root")
			dependencyRelease(t, f.repo, leaf, "2.0.0")
			dependencyRelease(t, f.repo, leaf, "3.0.0")
			dependencyRelease(t, f.repo, parent, "1.1.0", leaf+"@^2")
			f.upgrade(t, "", false)
			m := loadManifest(t, f.path)
			assertResolved(t, m.Skills[parent], "", "1.1.0")
			assertResolved(t, m.Skills[leaf], "", "2.0.0")
		})
	}
}
