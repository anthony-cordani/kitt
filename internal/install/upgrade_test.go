package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestUpgradeStopsAtCurrentMajorAndNotesNext(t *testing.T) {
	project, skills := newPair(t)
	testutil.Release(t, skills, "review", "1.2.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review@^1.2")
	testutil.Release(t, skills, "review", "1.4.0")
	testutil.Release(t, skills, "review", "2.0.0")

	out := mustUpgrade(t, project, "review", false)

	for _, want := range []string{
		"+review 1.4.0",
		"note: review 2.0.0 is available (major upgrade, use --major)",
		"upgraded review 1.2.0 -> 1.4.0",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q\n%s", want, out)
		}
	}
	assertResolved(t, skillEntry(t, project, "review"), "^1.4.0", "1.4.0")
	body := readText(t, filepath.Join(skillPath(project, "review"), "SKILL.md"))
	if !strings.Contains(body, "\nreview 1.4.0\n") || strings.Contains(body, "review 1.2.0") {
		t.Fatalf("installed skill =\n%s", body)
	}
}

func TestUpgradeMajorThenAllUpToDate(t *testing.T) {
	project, skills := newPair(t)
	testutil.Release(t, skills, "review", "1.2.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review@^1.2")
	testutil.Release(t, skills, "review", "1.4.0")
	testutil.Release(t, skills, "review", "2.0.0")

	out := mustUpgrade(t, project, "review", true)

	if !strings.Contains(out, "upgraded review 1.2.0 -> 2.0.0") {
		t.Fatalf("output = %q", out)
	}
	assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")

	again := mustUpgrade(t, project, "", false)

	if !strings.Contains(again, "up to date review 2.0.0") {
		t.Fatalf("output = %q", again)
	}
	assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")
}

func TestUpgradeKeepsExactPin(t *testing.T) {
	project, skills := newPair(t)
	release(t, skills, "review", "1.2.0", "1.4.0", "2.0.0")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review@1.2.0")
	before := snapshot(t, project)

	out := mustUpgrade(t, project, "review", false)

	if !strings.Contains(out, "kept review 1.2.0 (exact pin in kitt.toml)") {
		t.Fatalf("output = %q", out)
	}
	assertSameTree(t, before, snapshot(t, project))
	assertResolved(t, skillEntry(t, project, "review"), "1.2.0", "1.2.0")
}

func TestUpgradeUnknownSkillErrors(t *testing.T) {
	project, _ := standard(t)
	opt, _ := withOpts(project, false)

	err := install.Upgrade(opt, "missing", false)

	if err == nil || !strings.Contains(err.Error(), "skill missing is not in") {
		t.Fatalf("Upgrade err = %v", err)
	}
}

func TestUpgradeDegradedSkillInstallsNewCommit(t *testing.T) {
	project, skills := newPair(t)
	testutil.WriteSkill(t, skills, "review", "", "review base")
	first := testutil.Commit(t, skills, "base")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	mustAdd(t, project, "demo/review")
	if got := skillEntry(t, project, "review").Resolved.Commit; got != first {
		t.Fatalf("pinned commit = %s, want %s", got, first)
	}
	testutil.WriteSkill(t, skills, "review", "", "review next")
	next := testutil.Commit(t, skills, "next")

	out := mustUpgrade(t, project, "review", false)

	if !strings.Contains(out, "upgraded review ") {
		t.Fatalf("output = %q", out)
	}
	sk := skillEntry(t, project, "review")
	if sk.Resolved == nil || sk.Resolved.Commit != next {
		t.Fatalf("resolved commit = %+v, want %s", sk.Resolved, next)
	}
	body := readText(t, filepath.Join(skillPath(project, "review"), "SKILL.md"))
	if !strings.Contains(body, "\nreview next\n") {
		t.Fatalf("installed skill =\n%s", body)
	}
}

func TestListShowsInstalledLabelAndAvailable(t *testing.T) {
	project, skills := standard(t)
	testutil.WriteSkill(t, skills, "notes", "", "notes body")
	testutil.Commit(t, skills, "notes")
	mustAdd(t, project, "demo/review@^1")

	out := mustList(t, project)

	if !strings.Contains(out, "Installed:\n") {
		t.Fatalf("output = %q", out)
	}
	if !lineWith(out, "review", "1.4.0", "demo") {
		t.Fatalf("installed line missing\n%s", out)
	}
	if !strings.Contains(out, "Available in demo:\n") {
		t.Fatalf("output = %q", out)
	}
	if !lineWith(out, "review", "2.0.0", "installed") {
		t.Fatalf("available review line missing\n%s", out)
	}
	if !lineWith(out, "notes", "no release") {
		t.Fatalf("available notes line missing\n%s", out)
	}
}

func TestListUnreachableSourceDoesNotFail(t *testing.T) {
	project, skills := standard(t)
	missing := filepath.Join(project, "no-such-repo")
	writeKitt(t, project, map[string]string{
		"demo": testutil.URL(skills),
		"gone": testutil.URL(missing),
	})
	mustAdd(t, project, "demo/review@^1")

	out := mustList(t, project)

	if !strings.Contains(out, "Available in demo:\n") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(out, "Available in gone: unreachable") {
		t.Fatalf("output = %q", out)
	}
}
