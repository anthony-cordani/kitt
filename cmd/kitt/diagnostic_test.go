package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func diagnosticProject(t *testing.T) (string, string) {
	t.Helper()
	testutil.IsolateHome(t)
	project, repo := testutil.NewRepo(t), testutil.NewRepo(t)
	testutil.Release(t, repo, "leaf", "1.0.0")
	testutil.WriteSkillRequires(t, repo, "root", "1.0.0", []string{"leaf@^1"})
	testutil.Commit(t, repo, "root")
	testutil.Git(t, repo, "tag", "root/v1.0.0")
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Project\n")
	testutil.WriteFile(t, filepath.Join(project, manifest.FileName), fmt.Sprintf("[sources]\ndemo = %q\n", testutil.URL(repo)))
	if err := install.Add(install.Options{Root: project, Out: io.Discard}, "root@^1"); err != nil {
		t.Fatal(err)
	}
	return project, repo
}

func TestRunListDependencyTree(t *testing.T) {
	project, _ := diagnosticProject(t)
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"list"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("list exit %d: %s", code, &stderr)
	}
	tree, _, _ := strings.Cut(stdout.String(), "Available in ")
	const want = "Installed:\n  demo/root@1.0.0 (requested ^1)\n  `-- demo/leaf@1.0.0 (requires ^1)\n"
	if tree != want || !strings.Contains(stdout.String(), "Available in demo:") {
		t.Fatalf("list:\n%s\nwant tree:\n%s", &stdout, want)
	}
}

func TestRunDoctorDependencyExitCodesOfflineAndReadOnly(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(fmt.Sprint(broken), func(t *testing.T) {
			project, repo := diagnosticProject(t)
			if broken {
				path := filepath.Join(project, manifest.FileName)
				m, err := manifest.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				delete(m.Skills, "leaf")
				if err := m.Save(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.RemoveAll(repo); err != nil {
				t.Fatal(err)
			}
			cache, err := os.UserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(cache, "kitt")); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", t.TempDir())
			t.Chdir(project)
			home, err := os.UserHomeDir()
			if err != nil {
				t.Fatal(err)
			}
			beforeProject, beforeHome := snapshotFiles(t, project), snapshotFiles(t, home)
			code, stdout, stderr := runDoctor()
			wantCode, wantOut := 0, "ok\n"
			if broken {
				wantCode = 2
				wantOut = "missing dependency entry: root requires \"demo/leaf@^1\" (restore skills.leaf in the manifest)\n1 problem(s)\n"
			}
			if code != wantCode || stdout != wantOut || stderr != "" {
				t.Fatalf("doctor exit %d, want %d\nstdout: %s\nstderr: %s", code, wantCode, stdout, stderr)
			}
			if !reflect.DeepEqual(beforeProject, snapshotFiles(t, project)) || !reflect.DeepEqual(beforeHome, snapshotFiles(t, home)) {
				t.Fatal("doctor changed project or home files")
			}
		})
	}
}
