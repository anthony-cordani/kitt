package install_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

const healthyAgents = "# Project\n\n<!-- kitt:bootstrap -->\n\n<!-- kitt:docs:start -->\n<!-- kitt:docs:end -->\n"

func healthyProject(t *testing.T) string {
	t.Helper()
	project, skills := newPair(t)
	testutil.Release(t, skills, "review", "1.4.0")
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), healthyAgents)
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review")
	return project
}

func assertSingleProblem(t *testing.T, project, message string) {
	t.Helper()
	n, out := mustDoctor(t, project)
	if n != 1 {
		t.Fatalf("problems = %d, want 1\n%s", n, out)
	}
	if strings.Count(out, message) != 1 {
		t.Fatalf("message %q count = %d\n%s", message, strings.Count(out, message), out)
	}
	if !strings.Contains(out, "1 problem(s)\n") {
		t.Fatalf("summary missing:\n%s", out)
	}
}

func TestDoctorHealthyProject(t *testing.T) {
	project := healthyProject(t)

	n, out := mustDoctor(t, project)

	if n != 0 || out != "ok\n" {
		t.Fatalf("Doctor = %d, output = %q", n, out)
	}
}

func TestDoctorModifiedSkill(t *testing.T) {
	project := healthyProject(t)
	testutil.WriteFile(t, filepath.Join(skillPath(project, "review"), "LOCAL.md"), "local\n")

	assertSingleProblem(t, project, "modified: review (local changes; kitt install restores the pinned version)")
}

func TestDoctorMissingSkill(t *testing.T) {
	project := healthyProject(t)
	if err := os.RemoveAll(skillPath(project, "review")); err != nil {
		t.Fatal(err)
	}

	assertSingleProblem(t, project, "missing: review (run kitt install)")
}

func TestDoctorMissingLink(t *testing.T) {
	project := healthyProject(t)
	if err := os.Remove(claudePath(project, "review")); err != nil {
		t.Fatal(err)
	}

	assertSingleProblem(t, project, "missing link: .claude/skills/review (run kitt install)")
}

func TestDoctorForeignEntry(t *testing.T) {
	project := healthyProject(t)
	link := claudePath(project, "review")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(link, "USER.md"), "mine\n")

	assertSingleProblem(t, project, "foreign entry: .claude/skills/review is not a kitt link")
}

func TestDoctorOutdatedGitignore(t *testing.T) {
	project := healthyProject(t)
	testutil.WriteFile(t, filepath.Join(project, ".gitignore"), "nope\n")

	assertSingleProblem(t, project, "outdated: .gitignore kitt block (run kitt install)")
}

func TestDoctorMissingClaudeMD(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "demo/review@^1")
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "# Agents\n")

	assertSingleProblem(t, project, "missing: CLAUDE.md (run kitt install)")
}

func TestDoctorOutdatedDocsIndex(t *testing.T) {
	project := healthyProject(t)
	path := filepath.Join(project, "AGENTS.md")
	body := readText(t, path)
	const line = "- _No project docs yet._\n"
	if !strings.Contains(body, line) {
		t.Fatalf("setup index missing:\n%s", body)
	}
	testutil.WriteFile(t, path, strings.Replace(body, line, "- wrong\n", 1))

	assertSingleProblem(t, project, "outdated: AGENTS.md docs index (run kitt install)")
}

func TestDoctorStaleDoc(t *testing.T) {
	project, skills := newPair(t)
	testutil.Release(t, skills, "review", "1.0.0")
	app := filepath.Join(project, "src", "app.go")
	testutil.WriteFile(t, app, "package app\n")
	verified := testutil.Commit(t, project, "app")
	writeFlowDoc(t, project, fmt.Sprintf("title: Flow\ndescription: A flow\npaths:\n  - src/app.go\nverified: %s\n", verified))
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review")
	testutil.WriteFile(t, app, "package app\n// edited\n")
	testutil.Commit(t, project, "edit app")

	assertSingleProblem(t, project, fmt.Sprintf(
		"stale doc: .agents/docs/flow.md (src/app.go changed since %s)", verified[:7],
	))
}

func TestDoctorUnverifiedDoc(t *testing.T) {
	cases := []struct {
		name, front, want string
	}{
		{
			name:  "missing",
			front: "title: Flow\ndescription: A flow\npaths:\n  - src/app.go\n",
			want:  "unverified doc: .agents/docs/flow.md (set verified to a commit)",
		},
		{
			name:  "not hex",
			front: "title: Flow\ndescription: A flow\npaths:\n  - src/app.go\nverified: not-a-commit\n",
			want:  "unverified doc: .agents/docs/flow.md (verified is not a commit id)",
		},
		{
			name:  "unknown commit",
			front: "title: Flow\ndescription: A flow\npaths:\n  - src/app.go\nverified: abcdefabcdefabcdefabcdefabcdefabcdefabcd\n",
			want:  "unverified doc: .agents/docs/flow.md (commit abcdefa not found)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, skills := newPair(t)
			testutil.Release(t, skills, "review", "1.0.0")
			testutil.WriteFile(t, filepath.Join(project, "src", "app.go"), "package app\n")
			testutil.Commit(t, project, "app")
			writeFlowDoc(t, project, tc.front)
			writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
			mustAdd(t, project, "demo/review")

			assertSingleProblem(t, project, tc.want)
		})
	}
}

func TestDoctorNotPinned(t *testing.T) {
	project := healthyProject(t)
	path := filepath.Join(project, "kitt.toml")
	m := loadManifest(t, path)
	m.Skills["review"].Resolved = nil
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}

	assertSingleProblem(t, project, "not pinned: review (run kitt install)")
}

func TestDoctorChangesNothing(t *testing.T) {
	project := healthyProject(t)
	testutil.WriteFile(t, filepath.Join(skillPath(project, "review"), "LOCAL.md"), "local\n")
	before := snapshot(t, project)

	n, out := mustDoctor(t, project)

	if n == 0 {
		t.Fatalf("Doctor reported no problem:\n%s", out)
	}
	assertSameTree(t, before, snapshot(t, project))
}

func writeFlowDoc(t *testing.T, project, front string) {
	t.Helper()
	testutil.WriteFile(t, filepath.Join(project, "AGENTS.md"), "BEFORE\n<!-- kitt:docs:start -->\n<!-- kitt:docs:end -->\nAFTER\n")
	testutil.WriteFile(t, filepath.Join(project, ".agents", "docs", "flow.md"), "---\n"+front+"---\nThe flow.\n")
}
