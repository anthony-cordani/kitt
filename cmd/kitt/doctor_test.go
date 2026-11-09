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
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func doctorOutput(t *testing.T, args []string, wantCode int, wantOut, wantErr string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"doctor"}, args...), &stdout, &stderr)
	if code != wantCode || stdout.String() != wantOut || stderr.String() != wantErr {
		t.Fatalf("doctor %v = %d\nstdout: %q\nstderr: %q\nwant: %d, %q, %q", args, code, stdout.String(), stderr.String(), wantCode, wantOut, wantErr)
	}
}

func TestRunDoctorDocsWithoutManifest(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("GITHUB_ACTIONS", "false")
	project := testutil.NewRepo(t)
	t.Chdir(project)
	app := filepath.Join(project, "app.go")
	testutil.WriteFile(t, app, "package app\n")
	verified := testutil.Commit(t, project, "app")
	doc := fmt.Sprintf("---\npaths: [app.go]\nverified: %s\n---\nFlow\n", verified)
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "flow.md"), doc)
	doctorOutput(t, []string{"--docs"}, 0, "ok\n", "")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, "", "")
	testutil.WriteFile(t, app, "package app\n// changed\n")
	testutil.Commit(t, project, "change app")
	problem := fmt.Sprintf("stale doc: .agents/docs/flow.md (app.go changed since %s)\n", verified[:7])
	doctorOutput(t, []string{"--docs"}, 2, problem+"1 problem(s)\n", "")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, problem+"1 problem(s)\n", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, "::warning::"+problem+"1 problem(s)\n", "")
	doctorOutput(t, []string{"-g", "--docs"}, 1, "", "kitt doctor: --docs applies to a project, not to -g\n")
	for _, name := range []string{"kitt.toml", ".agents/skills"} {
		if _, err := os.Stat(filepath.Join(project, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", name, err)
		}
	}
}

func TestRunDoctorDocsOutsideProject(t *testing.T) {
	testutil.IsolateHome(t)
	root := t.TempDir()
	t.Chdir(root)
	wantErr := fmt.Sprintf("kitt doctor: no AGENTS.md or .agents/docs in %s, run from the project root\n", root)
	doctorOutput(t, []string{"--docs"}, 1, "", wantErr)
	doctorOutput(t, []string{"--docs", "--warn"}, 1, "", wantErr)
}

func TestRunDoctorDocsIgnoresModifiedSkill(t *testing.T) {
	reviewProject(t)
	t.Setenv("GITHUB_ACTIONS", "false")
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(root, "AGENTS.md"), "# Project\n")
	if err := install.Add(install.Options{Root: root, Out: io.Discard}, "review@^1"); err != nil {
		t.Fatal(err)
	}
	doctorOutput(t, nil, 0, "ok\n", "")
	doctorOutput(t, []string{"--warn"}, 0, "", "")
	testutil.WriteFile(t, filepath.Join(root, ".agents", "skills", "review", "LOCAL.md"), "local\n")
	doctorOutput(t, []string{"--docs"}, 0, "ok\n", "")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, "", "")
	problem := "modified: review (local changes; kitt install restores the pinned version)\n1 problem(s)\n"
	doctorOutput(t, []string{"--warn"}, 0, problem, "")
	doctorOutput(t, nil, 2, problem, "")
	t.Setenv("GITHUB_ACTIONS", "true")
	doctorOutput(t, []string{"--warn"}, 0, "::warning::"+problem, "")
}

func TestRunDoctorWarnError(t *testing.T) {
	testutil.IsolateHome(t)
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"--warn"}, {"--docs", "--warn"}} {
		if len(args) == 2 {
			if err := os.Mkdir("AGENTS.md", 0o755); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		code := run(append([]string{"doctor"}, args...), &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "kitt doctor: ") {
			t.Fatalf("doctor %v = %d, %q, %q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunDoctorDocsChecksIndexOnly(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("GITHUB_ACTIONS", "true")
	root := testutil.NewRepo(t)
	t.Chdir(root)
	// These project files would fail the full check, but are outside docs mode.
	testutil.WriteFile(t, filepath.Join(root, "kitt.toml"), "invalid manifest [\n")
	for _, name := range []string{".gitignore", "CLAUDE.md", ".agents/skills", ".claude/skills"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const agents = "<!-- kitt:bootstrap -->\n<!-- kitt:docs:start -->\n- _No project docs yet._\n<!-- kitt:docs:end -->\n"
	testutil.WriteFile(t, filepath.Join(root, "AGENTS.md"), agents)
	doctorOutput(t, []string{"--docs"}, 0, "ok\n", "")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, "", "")
	testutil.WriteFile(t, filepath.Join(root, ".agents", "docs", "flow.md"), "---\ntitle: Flow\ndescription: A flow\npaths: [app.go]\n---\nFlow\n")
	const indexProblem = "outdated: AGENTS.md docs index (run kitt install)\n"
	const docProblem = "unverified doc: .agents/docs/flow.md (set verified to a commit)\n"
	doctorOutput(t, []string{"--docs"}, 2, indexProblem+docProblem+"2 problem(s)\n", "")
	doctorOutput(t, []string{"--docs", "--warn"}, 0, "::warning::"+indexProblem+"::warning::"+docProblem+"2 problem(s)\n", "")
}
