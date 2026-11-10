package scaffold

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestInitAddsAndRefreshesDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "adr", "1.0.0")
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), "skills = [\"adr\"]\n")
	testutil.Commit(t, repo, "initial defaults")
	var out bytes.Buffer
	if err := Init(Options{Root: project, Sources: map[string]string{"demo": testutil.URL(repo)}, Out: &out}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, manifest.FileName)
	before, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Skills) != 1 || before.Skills["adr"] == nil || before.Skills["adr"].Version != "^1.0.0" || !strings.Contains(out.String(), "added default demo/adr\n") {
		t.Fatalf("fresh defaults = %+v, output:\n%s", before.Skills, &out)
	}
	if !strings.Contains(readFile(t, filepath.Join(project, ".agents", "skills", "adr", "SKILL.md")), "adr 1.0.0") {
		t.Fatal("fresh default not installed")
	}

	// A newer release of an existing default must not change its pin on refresh.
	testutil.Release(t, repo, "adr", "1.1.0")
	testutil.Release(t, repo, "code-review", "2.0.0")
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), "skills = [\"adr\", \"code-review\"]\n")
	testutil.Commit(t, repo, "new default")
	out.Reset()
	// Re-running init uses the sources already in kitt.toml.
	if err := Init(Options{Root: project, Out: &out}); err != nil {
		t.Fatal(err)
	}
	after, err := manifest.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Skills) != 2 || after.Skills["code-review"] == nil || after.Skills["code-review"].Version != "^2.0.0" {
		t.Fatalf("refreshed defaults = %+v", after.Skills)
	}
	if !reflect.DeepEqual(before.Skills["adr"], after.Skills["adr"]) {
		t.Fatalf("existing pin changed: before %+v, after %+v", before.Skills["adr"], after.Skills["adr"])
	}
	if strings.Count(out.String(), "added default ") != 1 || !strings.Contains(out.String(), "added default demo/code-review\n") {
		t.Fatalf("refresh output:\n%s", &out)
	}
}

func TestInitNoDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "adr", "1.0.0")
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), "skills = [\"adr\"]\n")
	testutil.Commit(t, repo, "defaults")
	var out bytes.Buffer
	if err := Init(Options{Root: project, Sources: map[string]string{"demo": testutil.URL(repo)}, NoDefaults: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Skills) != 0 || strings.Contains(out.String(), "default") {
		t.Fatalf("NoDefaults skills = %+v, output:\n%s", m.Skills, &out)
	}

	// Opting out applies only to that run.
	if err := Init(Options{Root: project}); err != nil {
		t.Fatal(err)
	}
	m, err = manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if m.Skills["adr"] == nil {
		t.Fatal("default not added on next init")
	}
}

func TestInitInvalidDefaultsWarnsAndContinues(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	repo := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(repo, "kitt-defaults.toml"), "skills = [")
	testutil.Commit(t, repo, "invalid defaults")
	var out bytes.Buffer
	if err := Init(Options{Root: project, Sources: map[string]string{"demo": testutil.URL(repo)}, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "warning: source demo defaults:") || !strings.Contains(out.String(), nextLine) {
		t.Fatalf("output:\n%s", &out)
	}
	m, err := manifest.Load(filepath.Join(project, manifest.FileName))
	if err != nil || len(m.Skills) != 0 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
}
