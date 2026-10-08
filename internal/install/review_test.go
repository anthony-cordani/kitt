package install_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestOwnershipPreservesForeignSkillsAndLinks(t *testing.T) {
	project, _ := standard(t)
	for _, name := range []string{"custom", "kitt-bootstrap", ".kitt-tmp-kept"} {
		testutil.WriteFile(t, filepath.Join(skillPath(project, name), "note"), "user\n")
	}
	mustAdd(t, project, "review@^1")
	// Link creation is done by kitt so this test also runs on Windows without symlink privileges.
	if err := os.MkdirAll(filepath.Dir(claudePath(project, "custom")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(claudePath(project, "review"), claudePath(project, "custom")); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, project)
	m := loadManifest(t, filepath.Join(project, manifest.FileName))
	delete(m.Skills, "review")
	if err := m.Save(filepath.Join(project, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
	out := mustRestore(t, project)
	for _, name := range []string{"custom", "kitt-bootstrap"} {
		want := fmt.Sprintf("warning: .agents/skills/%s is not managed by kitt, left untouched\n", name)
		if strings.Count(out, want) != 1 {
			t.Fatalf("warning count for %s: %q", name, out)
		}
		if readText(t, filepath.Join(skillPath(project, name), "note")) != "user\n" {
			t.Fatal("foreign skill changed")
		}
	}
	if strings.Contains(out, ".kitt-tmp-") || strings.Contains(out, ".kitt-installed") {
		t.Fatalf("internal entries warned about: %s", out)
	}
	if _, err := os.Lstat(claudePath(project, "custom")); err != nil {
		t.Fatalf("foreign link removed: %v", err)
	}
	assertMissing(t, skillPath(project, "review"))
	assertMissing(t, claudePath(project, "review"))
	if got := readText(t, skillPath(project, ".kitt-installed")); got != "" {
		t.Fatalf("state = %q", got)
	}
}

func TestOwnershipRefusesExistingSkill(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, command := range []string{"add", "restore", "upgrade"} {
			for _, stateExists := range []bool{false, true} {
				t.Run(fmt.Sprintf("global=%t/%s/state=%t", global, command, stateExists), func(t *testing.T) {
					home := testutil.IsolateHome(t)
					project := testutil.NewRepo(t)
					repo := testutil.NewRepo(t)
					testutil.Release(t, repo, "review", "1.0.0")
					mf := filepath.Join(project, manifest.FileName)
					root := project
					if global {
						var err error
						mf, err = manifest.GlobalPath()
						if err != nil {
							t.Fatal(err)
						}
						root = home
					}
					testutil.WriteFile(t, mf, fmt.Sprintf("[sources]\ndemo = %q\n[skills.review]\nsource = \"demo\"\nversion = \"^1\"\n", testutil.URL(repo)))
					testutil.WriteFile(t, filepath.Join(skillPath(root, "review"), "note"), "foreign\n")
					if stateExists {
						testutil.WriteFile(t, skillPath(root, ".kitt-installed"), "")
					}
					before := snapshot(t, root)
					manifestBefore := readText(t, mf)
					opts, _ := withOpts(project, global)
					var err error
					switch command {
					case "add":
						err = install.Add(opts, "review")
					case "restore":
						err = install.Restore(opts)
					case "upgrade":
						err = install.Upgrade(opts, "review", false)
					}
					want := ".agents/skills/review exists and was not installed by kitt: move it away or remove it"
					if err == nil || err.Error() != want {
						t.Fatalf("error = %v, want %s", err, want)
					}
					if readText(t, mf) != manifestBefore {
						t.Fatal("manifest changed")
					}
					if !global {
						assertSameTree(t, before, snapshot(t, root))
					}
					if readText(t, filepath.Join(skillPath(root, "review"), "note")) != "foreign\n" {
						t.Fatal("foreign skill changed")
					}
				})
			}
		}
	}
}

func TestOwnershipRefusesUnownedMatchingPin(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "review@^1")
	testutil.WriteFile(t, skillPath(project, ".kitt-installed"), "")
	before := snapshot(t, project)
	opts, _ := withOpts(project, false)
	err := install.Restore(opts)
	if err == nil || !strings.Contains(err.Error(), "was not installed by kitt") {
		t.Fatalf("error = %v", err)
	}
	assertSameTree(t, before, snapshot(t, project))
}

func TestOwnershipMigratesOnlyMatchingPins(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "review@^1")
	if err := os.Remove(skillPath(project, ".kitt-installed")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(skillPath(project, "custom"), "note"), "foreign\n")
	mustRestore(t, project)
	if got := readText(t, skillPath(project, ".kitt-installed")); got != "review\n" {
		t.Fatalf("state = %q", got)
	}
	if err := os.Remove(skillPath(project, ".kitt-installed")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(skillPath(project, "review"), "SKILL.md"), "changed\n")
	opts, _ := withOpts(project, false)
	err := install.Restore(opts)
	if err == nil || !strings.Contains(err.Error(), "was not installed by kitt") {
		t.Fatalf("error = %v", err)
	}
	if readText(t, filepath.Join(skillPath(project, "review"), "SKILL.md")) != "changed\n" {
		t.Fatal("unmatched directory overwritten")
	}
}

func TestOwnershipTracksSortedSkillsAndBootstrap(t *testing.T) {
	project, repo := standard(t)
	testutil.Release(t, repo, "adr", "1.0.0")
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n<!-- kitt:bootstrap -->\n")
	mustAdd(t, project, "review@^1")
	mustAdd(t, project, "adr")
	state := skillPath(project, ".kitt-installed")
	if got := readText(t, state); got != "adr\nkitt-bootstrap\nreview\n" {
		t.Fatalf("state = %q", got)
	}
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	mustRestore(t, project)
	after, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged state rewritten")
	}
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")
	mustRestore(t, project)
	if got := readText(t, state); got != "adr\nreview\n" {
		t.Fatalf("state = %q", got)
	}
	assertNoTempDirs(t, project)
}

func TestOwnershipRefusesForeignBootstrap(t *testing.T) {
	project, _ := standard(t)
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "<!-- kitt:bootstrap -->\n")
	testutil.WriteFile(t, filepath.Join(skillPath(project, "kitt-bootstrap"), "SKILL.md"), "foreign\n")
	opts, _ := withOpts(project, false)
	err := install.Restore(opts)
	if err == nil || err.Error() != ".agents/skills/kitt-bootstrap exists and was not installed by kitt: move it away or remove it" {
		t.Fatalf("error = %v", err)
	}
	if readText(t, filepath.Join(skillPath(project, "kitt-bootstrap"), "SKILL.md")) != "foreign\n" {
		t.Fatal("bootstrap overwritten")
	}
}

func TestManifestNamesValidatedBeforeAnyWrites(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, name := range []string{"../escape", "kitt-custom"} {
			for _, command := range []string{"restore", "upgrade", "list", "doctor"} {
				t.Run(fmt.Sprintf("global=%t/%s/%s", global, name, command), func(t *testing.T) {
					home := testutil.IsolateHome(t)
					project := testutil.NewRepo(t)
					repo := testutil.NewRepo(t)
					testutil.Release(t, repo, "aaa", "1.0.0")
					mf := filepath.Join(project, manifest.FileName)
					if global {
						var err error
						mf, err = manifest.GlobalPath()
						if err != nil {
							t.Fatal(err)
						}
					}
					m := &manifest.Manifest{Sources: map[string]string{"demo": testutil.URL(repo)}, Skills: map[string]*manifest.Skill{"aaa": {Source: "demo"}, name: {Source: "demo"}}}
					if err := m.Save(mf); err != nil {
						t.Fatal(err)
					}
					beforeProject, beforeHome := snapshot(t, project), snapshot(t, home)
					opts, out := withOpts(project, global)
					var err error
					switch command {
					case "restore":
						err = install.Restore(opts)
					case "upgrade":
						err = install.Upgrade(opts, "aaa", false)
					case "list":
						err = install.List(opts)
					case "doctor":
						_, err = install.Doctor(opts)
					}
					if err == nil || !strings.HasPrefix(err.Error(), fmt.Sprintf("invalid skill name in kitt.toml: %q: ", name)) {
						t.Fatalf("error = %v", err)
					}
					if out.Len() != 0 {
						t.Fatalf("output before validation: %q", out)
					}
					assertSameTree(t, beforeProject, snapshot(t, project))
					assertSameTree(t, beforeHome, snapshot(t, home))
				})
			}
		}
	}
}

func TestUpgradeUnresolvedCaretHonorsMajor(t *testing.T) {
	project, _ := standard(t)
	m := loadManifest(t, filepath.Join(project, manifest.FileName))
	m.Skills["review"] = &manifest.Skill{Source: "demo", Version: "^1.2"}
	if err := m.Save(filepath.Join(project, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
	mustUpgrade(t, project, "review", false)
	assertResolved(t, skillEntry(t, project, "review"), "^1.4.0", "1.4.0")
}

func TestUpgradeUnresolvedCaretHonorsFloor(t *testing.T) {
	project, _ := standard(t)
	m := loadManifest(t, filepath.Join(project, manifest.FileName))
	m.Skills["review"] = &manifest.Skill{Source: "demo", Version: "^1.9"}
	if err := m.Save(filepath.Join(project, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, project)
	opts, _ := withOpts(project, false)
	if err := install.Upgrade(opts, "review", false); err == nil {
		t.Fatal("installed a version outside the constraint")
	}
	assertSameTree(t, before, snapshot(t, project))
}

func TestUpgradeSavesSuccessBeforeLaterFailure(t *testing.T) {
	project, repo := newPair(t)
	testutil.Release(t, repo, "a", "1.0.0")
	testutil.WriteSkill(t, repo, "b", "", "b")
	testutil.Commit(t, repo, "b")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(repo)})
	mustAdd(t, project, "a")
	mustAdd(t, project, "b")
	next := testutil.Release(t, repo, "a", "1.1.0")
	if err := os.RemoveAll(filepath.Join(repo, "skills", "b")); err != nil {
		t.Fatal(err)
	}
	testutil.Commit(t, repo, "remove b")
	opts, _ := withOpts(project, false)
	if err := install.Upgrade(opts, "", false); err == nil || !strings.Contains(err.Error(), "skill b not found") {
		t.Fatalf("error = %v", err)
	}
	sk := skillEntry(t, project, "a")
	if sk.Resolved.Commit != next {
		t.Fatalf("manifest still pins %s, want %s", sk.Resolved.Commit, next)
	}
	if n, out := mustDoctor(t, project); n != 0 {
		t.Fatalf("doctor: %s", out)
	}
}

func TestRestoreAndDoctorHandleNonDirectorySkill(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			project, _ := standard(t)
			mustAdd(t, project, "review@^1")
			dest := skillPath(project, "review")
			if err := os.RemoveAll(dest); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "working")
			testutil.WriteFile(t, filepath.Join(target, "keep"), "foreign\n")
			if kind == "file" {
				testutil.WriteFile(t, dest, "file\n")
			} else if err := os.Symlink(target, dest); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if n, out := mustDoctor(t, project); n == 0 || !strings.Contains(out, "modified: review") {
				t.Fatalf("doctor: %s", out)
			}
			mustRestore(t, project)
			if n, out := mustDoctor(t, project); n != 0 {
				t.Fatalf("doctor after restore: %s", out)
			}
			if readText(t, filepath.Join(target, "keep")) != "foreign\n" {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestRestoreWithoutConstraintSavesCaret(t *testing.T) {
	project, _ := standard(t)
	m := loadManifest(t, filepath.Join(project, manifest.FileName))
	m.Skills["review"] = &manifest.Skill{Source: "demo"}
	if err := m.Save(filepath.Join(project, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
	mustRestore(t, project)
	assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")
}

func TestDocsIndexEscapesTitlesAndPaths(t *testing.T) {
	project, _ := standard(t)
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "<!-- kitt:docs:start -->\n<!-- kitt:docs:end -->\n")
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "sync flow.md"), "---\ntitle: 'Sync [flow]'\n---\n")
	mustRestore(t, project)
	want := `- [Sync \[flow\]](.agents/docs/sync%20flow.md)`
	if got := readText(t, filepath.Join(project, "AGENTS.md")); !strings.Contains(got, want) {
		t.Fatalf("index = %s; want %s", got, want)
	}
}

func TestGlobalAddRejectsReservedName(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "kitt-custom", "1.0.0")
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, mf, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	opts, _ := withOpts("", true)
	err = install.Add(opts, "kitt-custom")
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error = %v", err)
	}
	assertMissing(t, skillPath(home, "kitt-custom"))
}
