package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/scaffold"
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

func TestInstallSourceFlagAfterSkill(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "1.0.0", "review 1.0.0")
	testutil.Commit(t, repo, "review 1.0.0")
	testutil.Git(t, repo, "tag", "review/v1.0.0")
	t.Chdir(t.TempDir())
	var out, stderr bytes.Buffer
	if code := run([]string{"install", "review", "-g", "--source", "s=" + testutil.URL(repo)}, &out, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, &stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
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

func TestInstallSourceWithoutSkillLeavesOtherFilesByteIdentical(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	demoRepo := testutil.NewRepo(t)
	extraRepo := testutil.NewRepo(t)
	demoURL := testutil.URL(demoRepo)
	extraURL := testutil.URL(extraRepo)

	if err := scaffold.Init(scaffold.Options{
		Root:    project,
		Sources: map[string]string{"demo": demoURL},
		Out:     io.Discard,
	}); err != nil {
		t.Fatal(err)
	}

	// Customize .gitignore and add an unindexed doc so that if install.Restore
	// is mistakenly invoked, it will rewrite these kitt-managed files.
	testutil.WriteFile(t, filepath.Join(project, ".gitignore"), "/node_modules/\n")
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "notes.md"), "---\ntitle: Notes\n---\nnotes\n")

	before := snapshotFiles(t, project)
	if _, ok := before[".gitignore"]; !ok {
		t.Fatal("expected .gitignore in project snapshot")
	}
	if _, ok := before["AGENTS.md"]; !ok {
		t.Fatal("expected AGENTS.md in project snapshot")
	}

	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	code := run([]string{"install", "--source", "extra=" + extraURL}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}

	m, err := manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Sources["extra"]; got != extraURL {
		t.Fatalf("source extra = %q, want %q", got, extraURL)
	}
	if got := m.Sources["demo"]; got != demoURL {
		t.Fatalf("source demo = %q, want %q", got, demoURL)
	}

	after := snapshotFiles(t, project)

	delete(before, manifest.FileName)
	delete(after, manifest.FileName)

	if len(before) != len(after) {
		t.Fatalf("file count changed: before %d, after %d", len(before), len(after))
	}
	for path, beforeBytes := range before {
		afterBytes, ok := after[path]
		if !ok {
			t.Fatalf("file removed: %s", path)
		}
		if !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("file %s changed byte contents:\nbefore:\n%s\nafter:\n%s", path, beforeBytes, afterBytes)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Fatalf("file created: %s", path)
		}
	}
}

func snapshotFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			target, err := os.Readlink(path)
			if err != nil {
				target = "?"
			}
			out[rel] = []byte("link:" + target)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
