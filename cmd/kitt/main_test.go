package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/scaffold"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestRunHelpListsSixCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	for _, name := range []string{"init", "install", "upgrade", "list", "doctor", "release"} {
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
	for _, name := range []string{"init", "install", "upgrade", "list", "doctor", "release"} {
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

	t.Run("healthy", func(t *testing.T) {
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
