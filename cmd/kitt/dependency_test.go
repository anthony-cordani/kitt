package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInitRestoresDependencyClosure(t *testing.T) {
	testutil.IsolateHome(t)
	project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
	testutil.Release(t, repo, "leaf", "1.0.0")
	testutil.WriteSkillRequires(t, repo, "root", "1.0.0", []string{"leaf@^1"})
	testutil.Commit(t, repo, "root")
	testutil.Git(t, repo, "tag", "root/v1.0.0")
	path := filepath.Join(project, manifest.FileName)
	testutil.WriteFile(t, path, fmt.Sprintf("[sources]\ndemo = %q\n[skills.root]\nsource = 'demo'\n", testutil.URL(repo)))
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exit %d: %s", code, &stderr)
	}
	m, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	leaf := m.Skills["leaf"]
	if leaf == nil || leaf.IsDirect() || leaf.Resolved == nil || leaf.Resolved.Version != "1.0.0" {
		t.Fatalf("init did not restore dependency closure: %+v", leaf)
	}
}

func TestInstallClaimsNoChangesOnlyBeforeManagedWrites(t *testing.T) {
	for _, addSource := range []bool{false, true} {
		t.Run(fmt.Sprint(addSource), func(t *testing.T) {
			testutil.IsolateHome(t)
			project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
			testutil.WriteSkillRequires(t, repo, "root", "1.0.0", []string{"root@^1"})
			testutil.Commit(t, repo, "root cycle")
			testutil.Git(t, repo, "tag", "root/v1.0.0")
			path := filepath.Join(project, manifest.FileName)
			testutil.WriteFile(t, path, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
			t.Chdir(project)
			args := []string{"install", "demo/root"}
			if addSource {
				args = append(args, "--source", "extra="+testutil.URL(repo))
			}
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), "dependency cycle") || strings.Contains(stderr.String(), "no changes applied") == addSource {
				t.Fatalf("incorrect failure claim (source added=%t): %s", addSource, &stderr)
			}
			m, err := manifest.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, added := m.Sources["extra"]; added != addSource {
				t.Fatalf("source mutation = %t, want %t", added, addSource)
			}
		})
	}
}

func TestUpgradeRollbackSaysNoChangesApplied(t *testing.T) {
	testutil.IsolateHome(t)
	project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")
	manifestPath := filepath.Join(project, manifest.FileName)
	testutil.WriteFile(t, manifestPath, fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"install", "review"}, &stdout, &stderr); code != 0 {
		t.Fatalf("install exit %d: %s", code, &stderr)
	}
	testutil.Release(t, repo, "review", "1.1.0")
	skill := filepath.Join(project, ".agents", "skills", "review", "SKILL.md")
	beforeSkill, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	gitignore := filepath.Join(project, ".gitignore")
	testutil.WriteFile(t, gitignore, "user entry\n")
	if err := os.Chmod(gitignore, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gitignore, 0o644) })
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"upgrade", "review"}, &stdout, &stderr); code != 1 {
		t.Fatalf("upgrade exit %d, stderr %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "  no changes applied\n") || strings.Contains(stdout.String(), "upgraded") {
		t.Fatalf("stdout %q\nstderr %q", stdout.String(), stderr.String())
	}
	afterSkill, err := os.ReadFile(skill)
	if err != nil || string(afterSkill) != string(beforeSkill) || !strings.Contains(string(afterSkill), "review 1.0.0") {
		t.Fatalf("skill changed: %s, %v", afterSkill, err)
	}
	afterManifest, err := os.ReadFile(manifestPath)
	if err != nil || string(afterManifest) != string(beforeManifest) {
		t.Fatalf("manifest changed:\n%s\nwas\n%s\n%v", afterManifest, beforeManifest, err)
	}
}
