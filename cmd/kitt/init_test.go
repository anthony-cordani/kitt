package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInitNoDefaultsFlag(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "adr", "1.0.0")
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), "skills = [\"adr\"]\n")
	testutil.Commit(t, repo, "defaults")
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	if code := initCmd([]string{"--source", "demo=" + testutil.URL(repo), "--no-defaults"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, &stderr)
	}
	m, err := manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil || len(m.Skills) != 0 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if code := initCmd(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("refresh exit %d, stderr: %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "added default demo/adr\n") {
		t.Fatalf("stdout:\n%s", &stdout)
	}
}

func TestInitUsageIncludesNoDefaults(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := initCmd([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "Usage: kitt init [--source alias=url]... [--no-defaults]\n") || !strings.Contains(stderr.String(), "do not add the default skills of the sources") {
		t.Fatalf("usage:\n%s", &stderr)
	}
}
