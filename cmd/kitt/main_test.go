package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/scaffold"
	"github.com/anthony-cordani/kitt/internal/testutil"
	"github.com/anthony-cordani/kitt/internal/updatecheck"
)

func TestRunHelpListsEightCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	for _, name := range []string{"init", "install", "remote", "upgrade", "list", "show", "doctor", "release"} {
		if !strings.Contains(stdout.String(), "\n  "+name+" ") {
			t.Errorf("stdout does not list %s\n%s", name, stdout.String())
		}
	}
}

func TestRunWithNoArgsExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
}

func TestRunUnknownCommandPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"nope"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "unknown command \"nope\"") {
		t.Fatalf("stderr missing unknown command:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("stderr missing usage:\n%s", stderr.String())
	}
}

func TestRunSubcommandHelpExitsZero(t *testing.T) {
	for _, name := range []string{"init", "install", "remote", "upgrade", "list", "doctor", "release"} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{name, "-h"}, &stdout, &stderr); code != 0 {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRejectsBadArgumentCounts(t *testing.T) {
	testutil.IsolateHome(t)
	t.Chdir(t.TempDir())
	cases := [][]string{
		{"install", "a", "b"},
		{"upgrade", "a", "b"},
		{"list", "x"},
		{"doctor", "x"},
		{"release"},
		{"release", "a", "b"},
		{"init", "x"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit %d, want 1\nstderr: %s", code, stderr.String())
			}
		})
	}
}

func TestRunInitSourceFlagRequiresEquals(t *testing.T) {
	testutil.IsolateHome(t)
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := run([]string{"init", "--source", "bad"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "alias=url") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDoctorExitCodes(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	sources := map[string]string{"demo": testutil.URL(testutil.NewRepo(t))}
	if err := scaffold.Init(scaffold.Options{Root: project, Sources: sources, Out: io.Discard}); err != nil {
		t.Fatal(err)
	}

	t.Run("pending setup", func(t *testing.T) {
		t.Chdir(project)
		code, stdout, stderr := runDoctor()
		if code != 2 || !strings.Contains(stdout, "pending: AGENTS.md setup") {
			t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("healthy", func(t *testing.T) {
		agents := filepath.Join(project, "AGENTS.md")
		data, err := os.ReadFile(agents)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(data), "<!-- kitt:bootstrap -->\r\n", "", 1)
		body = strings.Replace(body, "<!-- kitt:bootstrap -->\n", "", 1)
		testutil.WriteFile(t, agents, body)
		t.Chdir(project)
		code, stdout, stderr := runDoctor()
		if code != 0 {
			t.Fatalf("exit %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("problem", func(t *testing.T) {
		if err := os.Remove(filepath.Join(project, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(project)
		code, stdout, stderr := runDoctor()
		if code != 2 {
			t.Fatalf("exit %d, want 2\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})
	t.Run("no manifest", func(t *testing.T) {
		t.Chdir(t.TempDir())
		code, stdout, stderr := runDoctor()
		if code != 1 {
			t.Fatalf("exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
	})
}

func runDoctor() (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor"}, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func reviewProject(t *testing.T) {
	t.Helper()
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	skills := testutil.NewRepo(t)
	for _, version := range []string{"1.0.0", "1.4.0", "2.0.0"} {
		testutil.Release(t, skills, "review", version)
	}
	testutil.WriteFile(t, filepath.Join(project, "kitt.toml"), "[sources]\ndemo = \""+testutil.URL(skills)+"\"\n")
	t.Chdir(project)
}

func TestRunShowPrintsHeader(t *testing.T) {
	t.Run("project", func(t *testing.T) {
		reviewProject(t)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"show", "review"}, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "review 2.0.0 (demo)") {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})
	t.Run("global", func(t *testing.T) {
		testutil.IsolateHome(t)
		repo := testutil.NewRepo(t)
		testutil.Release(t, repo, "review", "1.0.0")
		if err := install.AddSources(install.Options{Global: true, Out: io.Discard}, map[string]string{"demo": testutil.URL(repo)}); err != nil {
			t.Fatal(err)
		}
		t.Chdir(t.TempDir())
		var stdout, stderr bytes.Buffer
		if code := run([]string{"show", "-g", "review"}, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "review 1.0.0 (demo)") {
			t.Fatalf("stdout = %q", stdout.String())
		}
	})
}

func TestRunShowUsageAndUnknown(t *testing.T) {
	reviewProject(t)
	for _, args := range [][]string{{"show"}, {"show", "a", "b"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit %d, want 1\nstderr: %s", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), "Usage: kitt show") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"show", "nope"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "kitt show:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestVersion(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%s) = %d, stderr %q", arg, code, stderr.String())
		}
		if got := stdout.String(); !strings.HasPrefix(got, "kitt ") || strings.TrimSpace(got) == "kitt" {
			t.Fatalf("run(%s) stdout = %q, want \"kitt <version>\"", arg, got)
		}
	}
}

func TestRunWithUpdateNotice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		code   int
	}{
		{"version", []string{"--version"}, http.StatusOK, 0},
		{"command failure", []string{"nope"}, http.StatusInternalServerError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"tag_name":"v9.9.9"}`)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			now := time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)
			checker := &updatecheck.Checker{
				Current:   "0.4.0",
				URL:       server.URL,
				CachePath: filepath.Join(t.TempDir(), "update-check.json"),
				Hint:      "brew upgrade --cask anthony-cordani/tap/kitt",
				Out:       &stderr,
				Now:       func() time.Time { return now },
			}
			if code := runWithUpdateNotice(tc.args, &stdout, &stderr, checker); code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
			if tc.code == 0 {
				if got, want := stdout.String(), "kitt "+currentVersion()+"\n"; got != want {
					t.Errorf("stdout = %q, want %q", got, want)
				}
				want := "Kitt update available 0.4.0 -> 9.9.9 (run: brew upgrade --cask anthony-cordani/tap/kitt)\n"
				if got := stderr.String(); got != want {
					t.Errorf("stderr = %q, want %q", got, want)
				}
			} else {
				if stdout.Len() != 0 {
					t.Errorf("stdout = %q, want empty", stdout.String())
				}
				var want bytes.Buffer
				run(tc.args, io.Discard, &want)
				if got := stderr.String(); got != want.String() {
					t.Errorf("stderr = %q, want %q", got, want.String())
				}
				if strings.Contains(stderr.String(), "Kitt update available") {
					t.Errorf("unexpected update notice: %q", stderr.String())
				}
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestRunWithUpdateNoticeNil(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithUpdateNotice([]string{"--version"}, &stdout, &stderr, nil); code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if got, want := stdout.String(), "kitt "+currentVersion()+"\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}
