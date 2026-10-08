package install_test

import (
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

func TestRestoreAfterDeleteReinstallsSameHash(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	dir := skillPath(project, "review")
	before, err := skill.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(filepath.Join(project, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	out := mustRestore(t, project)

	if !strings.Contains(out, "installed review 1.4.0\n") {
		t.Fatalf("output = %q", out)
	}
	after, err := skill.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("hash = %s, want %s", after, before)
	}
}

func TestRestoreUnchangedIsOffline(t *testing.T) {
	project, skills := standard(t)
	mustAdd(t, project, "demo/review@^1")
	if err := os.RemoveAll(skills); err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(cache, "kitt")); err != nil {
		t.Fatal(err)
	}

	out := mustRestore(t, project)

	if out != "ok review 1.4.0\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestRestoreReinstallsModifiedSkill(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	dir := skillPath(project, "review")
	before, err := skill.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(dir, "SKILL.md"), "tampered\n")

	out := mustRestore(t, project)

	if !strings.Contains(out, "installed review 1.4.0\n") {
		t.Fatalf("output = %q", out)
	}
	after, err := skill.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("hash = %s, want pinned %s", after, before)
	}
	body := readText(t, filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(body, "\nreview 1.4.0\n") {
		t.Fatalf("skill was not restored:\n%s", body)
	}
}

func TestRestoreFalsifiedHashMismatch(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	dir := skillPath(project, "review")
	testutil.WriteFile(t, filepath.Join(dir, "extra.txt"), "keep\n")
	before := snapshot(t, dir)
	path := filepath.Join(project, manifest.FileName)
	m := loadManifest(t, path)
	m.Skills["review"].Resolved.Hash = "h1:not-the-real-hash"
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	opt, _ := withOpts(project, false)

	err := install.Restore(opt)

	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("Restore err = %v", err)
	}
	assertSameTree(t, before, snapshot(t, dir))
	assertNoTempDirs(t, project)
}

func TestRestorePinsHandWrittenEntry(t *testing.T) {
	project, skills := standard(t)
	testutil.WriteFile(t, filepath.Join(project, manifest.FileName), fmt.Sprintf(
		"[sources]\ndemo = %q\n\n[skills.review]\nsource = \"demo\"\nversion = \"^1\"\n",
		testutil.URL(skills),
	))

	out := mustRestore(t, project)

	if !strings.Contains(out, "pinned review 1.4.0\n") {
		t.Fatalf("output = %q", out)
	}
	assertResolved(t, skillEntry(t, project, "review"), "^1", "1.4.0")
	body := readText(t, filepath.Join(skillPath(project, "review"), "SKILL.md"))
	if !strings.Contains(body, "\nreview 1.4.0\n") {
		t.Fatalf("pinned skill =\n%s", body)
	}
}

func TestRestorePrunesSkillRemovedFromManifest(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	path := filepath.Join(project, manifest.FileName)
	m := loadManifest(t, path)
	delete(m.Skills, "review")
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	out := mustRestore(t, project)

	if !strings.Contains(out, "removed review\n") {
		t.Fatalf("output = %q", out)
	}
	assertMissing(t, skillPath(project, "review"))
	assertMissing(t, claudePath(project, "review"))
}

func TestRestoreKeepsUnmanagedClaudeDirs(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	custom := filepath.Join(project, ".claude", "skills", "custom", "note.txt")
	testutil.WriteFile(t, custom, "user\n")
	link := claudePath(project, "review")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(link, "note.txt")
	testutil.WriteFile(t, foreign, "foreign\n")

	out := mustRestore(t, project)

	if !strings.Contains(out, "warning:") || !strings.Contains(out, "not managed by kitt") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, link) {
		t.Fatalf("output does not name %s\n%s", link, out)
	}
	if readText(t, foreign) != "foreign\n" {
		t.Fatalf("foreign dir was replaced:\n%s", readText(t, foreign))
	}
	if readText(t, custom) != "user\n" {
		t.Fatal("user directory was removed")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("foreign directory was turned into a link")
	}
}

func TestSkillLinkResolvesToInstalledSkill(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")

	assertLinkResolves(t, claudePath(project, "review"), skillPath(project, "review"))
}

func TestGitignoreBlockPreservesOutsideBytes(t *testing.T) {
	project, _ := standard(t)
	path := filepath.Join(project, ".gitignore")
	const prefix = "# kept before\nlegacy\n"
	testutil.WriteFile(t, path, prefix)

	mustAdd(t, project, "demo/review@^1")

	got := readText(t, path)
	want := prefix + kittBlock([]string{"review"}, false)
	if got != want {
		t.Fatalf("gitignore =\n%s\nwant\n%s", got, want)
	}
	const suffix = "keep-after\n"
	if err := os.WriteFile(path, []byte(got+suffix), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	out := mustRestore(t, project)

	if out != "ok review 1.4.0\n" {
		t.Fatalf("output = %q", out)
	}
	if readText(t, path) != got+suffix {
		t.Fatalf("gitignore changed:\n%s", readText(t, path))
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(info.ModTime()) {
		t.Fatalf("gitignore rewritten: mtime %s -> %s", info.ModTime(), info2.ModTime())
	}
}

func TestProjectBootstrapInstallAndRemoval(t *testing.T) {
	project, _ := standard(t)
	agents := filepath.Join(project, "AGENTS.md")
	testutil.WriteFile(t, agents, "# Project\n\n<!-- kitt:bootstrap -->\n")

	mustAdd(t, project, "demo/review@^1")

	boot := skillPath(project, "kitt-bootstrap")
	if !strings.Contains(readText(t, filepath.Join(boot, "SKILL.md")), "name: kitt-bootstrap") {
		t.Fatal("kitt-bootstrap was not installed")
	}
	assertLinkResolves(t, claudePath(project, "kitt-bootstrap"), boot)
	if readText(t, filepath.Join(project, "CLAUDE.md")) != "@AGENTS.md\n" {
		t.Fatalf("CLAUDE.md = %q", readText(t, filepath.Join(project, "CLAUDE.md")))
	}
	block := kittBlock([]string{"kitt-bootstrap", "review"}, true)
	if readText(t, filepath.Join(project, ".gitignore")) != block {
		t.Fatalf("gitignore =\n%s\nwant\n%s", readText(t, filepath.Join(project, ".gitignore")), block)
	}

	testutil.WriteFile(t, agents, "# Project\n\nmarker gone\n")
	mustRestore(t, project)

	assertMissing(t, boot)
	assertMissing(t, claudePath(project, "kitt-bootstrap"))
	block = kittBlock([]string{"review"}, true)
	if readText(t, filepath.Join(project, ".gitignore")) != block {
		t.Fatalf("gitignore after removal =\n%s\nwant\n%s", readText(t, filepath.Join(project, ".gitignore")), block)
	}
	if _, err := os.Lstat(skillPath(project, "review")); err != nil {
		t.Fatal(err)
	}
}

func TestExistingClaudeMDIsUntouched(t *testing.T) {
	project, _ := standard(t)
	const custom = "# custom claude\n"
	testutil.WriteFile(t, filepath.Join(project, "CLAUDE.md"), custom)
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")

	mustAdd(t, project, "demo/review@^1")

	if readText(t, filepath.Join(project, "CLAUDE.md")) != custom {
		t.Fatalf("CLAUDE.md = %q", readText(t, filepath.Join(project, "CLAUDE.md")))
	}
	got := readText(t, filepath.Join(project, ".gitignore"))
	if got != kittBlock([]string{"review"}, false) {
		t.Fatalf("gitignore =\n%s", got)
	}
	if strings.Contains(got, "/CLAUDE.md") {
		t.Fatalf("CLAUDE.md is in the block:\n%s", got)
	}
}

func TestDocsIndexUsesNameOrderAndKeepsOutside(t *testing.T) {
	project, _ := standard(t)
	agents := filepath.Join(project, "AGENTS.md")
	const outside = "BEFORE-INDEX\n<!-- kitt:docs:start -->\nPLACEHOLDER\n<!-- kitt:docs:end -->\nAFTER-INDEX\n"
	testutil.WriteFile(t, agents, outside)
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "a.md"), "---\ntitle: Zulu\ndescription: Last title\n---\nBody of a.\n")
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "b.md"), "# Plain\n")

	mustAdd(t, project, "demo/review@^1")

	index := "- [Zulu](.agents/docs/a.md) \u2014 Last title\n- [b](.agents/docs/b.md)\n"
	want := "BEFORE-INDEX\n<!-- kitt:docs:start -->\n" + index + "<!-- kitt:docs:end -->\nAFTER-INDEX\n"
	if got := readText(t, agents); got != want {
		t.Fatalf("AGENTS.md =\n%s\nwant\n%s", got, want)
	}
}

func TestDocsIndexEmptySaysNoDocs(t *testing.T) {
	project, _ := standard(t)
	agents := filepath.Join(project, "AGENTS.md")
	testutil.WriteFile(t, agents, "BEFORE-INDEX\n<!-- kitt:docs:start -->\nPLACEHOLDER\n<!-- kitt:docs:end -->\nAFTER-INDEX\n")

	mustRestore(t, project)

	index := "- _No project docs yet._\n"
	want := "BEFORE-INDEX\n<!-- kitt:docs:start -->\n" + index + "<!-- kitt:docs:end -->\nAFTER-INDEX\n"
	if got := readText(t, agents); got != want {
		t.Fatalf("AGENTS.md =\n%s\nwant\n%s", got, want)
	}
}

func TestGlobalInstallKeepsUserSkillAndSkipsGitignore(t *testing.T) {
	home := testutil.IsolateHome(t)
	skills := testutil.NewRepo(t)
	testutil.Release(t, skills, "review", "1.4.0")
	userFile := filepath.Join(home, ".agents", "skills", "userdata", "keep.txt")
	testutil.WriteFile(t, userFile, "keep\n")
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, mf, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(skills)))
	opt, buf := withOpts("", true)
	if err := install.Add(opt, "demo/review"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "added review 1.4.0\n") {
		t.Fatalf("add output = %q", buf.String())
	}
	opt, restoreOut := withOpts("", true)
	if err := install.Restore(opt); err != nil {
		t.Fatal(err)
	}

	if readText(t, userFile) != "keep\n" {
		t.Fatal("pre-existing user skill was pruned")
	}
	if strings.Contains(restoreOut.String(), "removed") {
		t.Fatalf("global restore pruned:\n%s", restoreOut.String())
	}
	assertMissing(t, filepath.Join(home, ".gitignore"))
	dest := skillPath(home, "review")
	link := claudePath(home, "review")
	assertLinkResolves(t, link, dest)
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(target) {
		t.Fatalf("link target %q is not absolute", target)
	}
}
