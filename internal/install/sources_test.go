package install_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestAddSourcesCreatesUserManifestThenInstalls(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "1.0.0", "review 1.0.0")
	testutil.Commit(t, repo, "review 1.0.0")
	testutil.Git(t, repo, "tag", "review/v1.0.0")

	opt, buf := withOpts("", true)
	if err := install.AddSources(opt, map[string]string{"s": testutil.URL(repo)}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "created "+mf+"\n") || !strings.Contains(out, "added source s\n") {
		t.Fatalf("output = %q", out)
	}
	m := loadManifest(t, mf)
	if m.Sources["s"] != testutil.URL(repo) {
		t.Fatalf("source s = %q", m.Sources["s"])
	}

	opt, _ = withOpts("", true)
	if err := install.Add(opt, "review"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestAddSourcesRejectsDifferentURL(t *testing.T) {
	testutil.IsolateHome(t)
	a := testutil.NewRepo(t)
	b := testutil.NewRepo(t)
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, mf, "[sources]\ns = \""+testutil.URL(a)+"\"\n")
	before, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}

	opt, _ := withOpts("", true)
	err = install.AddSources(opt, map[string]string{"s": testutil.URL(b)})
	if err == nil || !strings.Contains(err.Error(), "already set") {
		t.Fatalf("AddSources err = %v", err)
	}
	after, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("manifest bytes changed:\n%s", after)
	}
}

func TestAddSourcesUnreachableURLCreatesNothing(t *testing.T) {
	testutil.IsolateHome(t)
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	opt, _ := withOpts("", true)
	err := install.AddSources(opt, map[string]string{"s": missing})
	if err == nil || !strings.Contains(err.Error(), "not a reachable git repository") {
		t.Fatalf("AddSources err = %v", err)
	}
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	assertMissing(t, mf)
}

func TestAddSourcesSameURLIsNoop(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, mf, "[sources]\ns = \""+testutil.URL(repo)+"\"\n")
	before, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}

	opt, buf := withOpts("", true)
	if err := install.AddSources(opt, map[string]string{"s": testutil.URL(repo)}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("output = %q", buf.String())
	}
	after, err := os.ReadFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("manifest bytes changed:\n%s", after)
	}
}

func TestAddSourcesProjectAddsToProjectManifestOnly(t *testing.T) {
	project, skills := standard(t)
	other := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# agents\n")
	testutil.WriteFile(t, filepath.Join(project, "RULES.md"), "# rules\n")
	testutil.WriteFile(t, filepath.Join(project, "CLAUDE.md"), "# claude\n")
	testutil.WriteFile(t, filepath.Join(project, ".gitignore"), "/node_modules/\n")
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "notes.md"), "notes\n")
	mf := filepath.Join(project, manifest.FileName)
	before := snapshot(t, project)
	delete(before, manifest.FileName)

	opt, buf := withOpts(project, false)
	if err := install.AddSources(opt, map[string]string{"other": testutil.URL(other)}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}
	if out := buf.String(); out != "added source other\n" {
		t.Fatalf("output = %q", out)
	}
	m := loadManifest(t, mf)
	if m.Sources["other"] != testutil.URL(other) || m.Sources["demo"] != testutil.URL(skills) {
		t.Fatalf("sources = %v", m.Sources)
	}
	after := snapshot(t, project)
	delete(after, manifest.FileName)
	assertSameTree(t, before, after)
	globalMf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	assertMissing(t, globalMf)
}

func TestAddSourcesProjectRejectsDifferentURL(t *testing.T) {
	project, _ := standard(t)
	other := testutil.NewRepo(t)
	before := snapshot(t, project)

	opt, _ := withOpts(project, false)
	err := install.AddSources(opt, map[string]string{"demo": testutil.URL(other)})
	if err == nil || !strings.Contains(err.Error(), "already set") {
		t.Fatalf("AddSources err = %v", err)
	}
	assertSameTree(t, before, snapshot(t, project))
}

func TestAddSourcesProjectWithoutManifestCreatesNothing(t *testing.T) {
	testutil.IsolateHome(t)
	project := t.TempDir()
	repo := testutil.NewRepo(t)

	opt, _ := withOpts(project, false)
	err := install.AddSources(opt, map[string]string{"s": testutil.URL(repo)})
	if err == nil || !strings.Contains(err.Error(), "no kitt.toml here: run kitt init --source <alias>=<url>") {
		t.Fatalf("AddSources err = %v", err)
	}
	assertSameTree(t, map[string]string{}, snapshot(t, project))
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	assertMissing(t, mf)
}
