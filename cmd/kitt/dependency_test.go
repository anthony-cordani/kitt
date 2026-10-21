package main

import (
	"bytes"
	"fmt"
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
