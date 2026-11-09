package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestPrePushHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git hook execution is skipped on Windows")
	}
	// Build before isolating HOME so Go does not leave a module cache in it.
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "kitt")
	buildKitt(t, bin)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stale doc", "ending in false", "kitt absent", "appended code reads refs"} {
		t.Run(name, func(t *testing.T) {
			testutil.IsolateHome(t)
			for _, key := range []string{"GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES"} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("GITHUB_ACTIONS", "false")
			bare := t.TempDir()
			testutil.Git(t, bare, "init", "--bare", "-q", "-b", "main")
			project := testutil.NewRepo(t)
			hook := filepath.Join(project, ".git", "hooks", "pre-push")
			if name == "ending in false" {
				testutil.WriteFile(t, hook, "#!/bin/sh\nfalse\n")
			}
			testutil.WriteFile(t, filepath.Join(project, "app.go"), "package app\n")
			verified := testutil.Commit(t, project, "app")
			testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "flow.md"), fmt.Sprintf("---\ntitle: Flow\ndescription: A flow\npaths: [app.go]\nverified: %s\n---\nFlow\n", verified))
			testutil.WriteFile(t, filepath.Join(project, "app.go"), "package app\n// changed\n")
			cmd := exec.Command(bin, "init", "--source", "demo="+testutil.URL(testutil.NewRepo(t)))
			cmd.Dir = project
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("kitt init: %v\n%s", err, out)
			}
			requireKittHook(t, hook)
			head := testutil.Commit(t, project, "setup and change app")
			testutil.Git(t, project, "remote", "add", "origin", bare)
			if name == "appended code reads refs" {
				f, err := os.OpenFile(hook, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, writeErr := f.WriteString("echo after-kitt-block >&2\nread local_ref local_sha remote_ref remote_sha\nprintf '%s %s %s %s\\n' \"$local_ref\" \"$local_sha\" \"$remote_ref\" \"$remote_sha\" > hook-refs.txt\n")
				closeErr := f.Close()
				if writeErr != nil || closeErr != nil {
					t.Fatalf("append hook: %v, %v", writeErr, closeErr)
				}
			}
			if name == "kitt absent" {
				toolsDir := t.TempDir()
				if err := os.Symlink(git, filepath.Join(toolsDir, "git")); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", toolsDir)
			}
			cmd = exec.Command(git, "-C", project, "push", "origin", "main")
			cmd.Dir = project
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if name == "ending in false" {
				if !strings.Contains(stderr.String(), "stale doc: .agents/docs/flow.md") {
					t.Fatalf("kitt block did not run: %s", &stderr)
				}
				if err == nil {
					t.Fatalf("push succeeded despite existing hook ending in false\nstdout: %s\nstderr: %s", &stdout, &stderr)
				}
				if refs := testutil.Git(t, project, "ls-remote", "origin", "refs/heads/main"); refs != "" {
					t.Fatalf("ref pushed despite hook failure: %s", refs)
				}
				return
			}
			if err != nil {
				t.Fatalf("git push: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if got := testutil.Git(t, project, "ls-remote", "origin", "refs/heads/main"); got != head+"\trefs/heads/main" {
				t.Fatalf("pushed ref = %q", got)
			}
			if name == "kitt absent" {
				for _, text := range []string{"kitt:", "stale doc", "problem(s)"} {
					if strings.Contains(stderr.String(), text) {
						t.Fatalf("kitt absent but hook printed %q: %s", text, &stderr)
					}
				}
			} else if !strings.Contains(stderr.String(), "stale doc: .agents/docs/flow.md") {
				t.Fatalf("missing stale doc warning on stderr: %s", &stderr)
			}
			if name == "appended code reads refs" {
				if !strings.Contains(stderr.String(), "after-kitt-block") {
					t.Fatalf("appended code did not run: %s", &stderr)
				}
				want := "refs/heads/main " + head + " refs/heads/main " + strings.Repeat("0", len(head)) + "\n"
				if got := readFile(t, filepath.Join(project, "hook-refs.txt")); got != want {
					t.Fatalf("hook stdin = %q, want %q", got, want)
				}
			}
		})
	}
}
