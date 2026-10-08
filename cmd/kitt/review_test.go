package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestUpgradeFlagAfterSkill(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	testutil.WriteFile(t, filepath.Join(project, manifest.FileName), fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	if err := install.Add(install.Options{Root: project}, "review"); err != nil {
		t.Fatal(err)
	}
	testutil.Release(t, repo, "review", "2.0.0")
	t.Chdir(project)
	var out, stderr bytes.Buffer
	if code := run([]string{"upgrade", "review", "--major"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	m, err := manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Skills["review"].Resolved.Version; got != "2.0.0" {
		t.Fatalf("version = %s", got)
	}
}

func TestInstallGlobalFlagAfterSkill(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	mf, err := manifest.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, mf, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	project := t.TempDir()
	t.Chdir(project)
	var out, stderr bytes.Buffer
	if code := run([]string{"install", "review", "-g"}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(project, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("project written: %v", err)
	}
}

func TestInstallFlagsRespectDoubleDash(t *testing.T) {
	testutil.IsolateHome(t)
	t.Chdir(t.TempDir())
	var out, stderr bytes.Buffer
	if code := run([]string{"install", "--", "review", "-g"}, &out, &stderr); code != 1 {
		t.Fatalf("exit %d", code)
	}
}
