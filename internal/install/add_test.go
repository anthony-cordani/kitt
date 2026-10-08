package install_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestAddPicksHighestCaretVersion(t *testing.T) {
	project, _ := standard(t)

	out := mustAdd(t, project, "demo/review@^1")

	if out != "added review 1.4.0\n" {
		t.Fatalf("output = %q", out)
	}
	body := readText(t, filepath.Join(skillPath(project, "review"), "SKILL.md"))
	if !strings.Contains(body, "\nreview 1.4.0\n") {
		t.Fatalf("installed skill =\n%s", body)
	}
	assertResolved(t, skillEntry(t, project, "review"), "^1", "1.4.0")
}

func TestAddWithoutConstraintSavesCaretOfLatest(t *testing.T) {
	project, _ := standard(t)

	out := mustAdd(t, project, "demo/review")

	if out != "added review 2.0.0\n" {
		t.Fatalf("output = %q", out)
	}
	assertResolved(t, skillEntry(t, project, "review"), "^2.0.0", "2.0.0")
}

func TestAddWithoutAliasUsesTheOnlySource(t *testing.T) {
	project, _ := standard(t)

	out := mustAdd(t, project, "review@^1")

	if out != "added review 1.4.0\n" {
		t.Fatalf("output = %q", out)
	}
	sk := skillEntry(t, project, "review")
	if sk.Source != "demo" {
		t.Fatalf("source = %q, want demo", sk.Source)
	}
	assertResolved(t, sk, "^1", "1.4.0")
}

func TestAddWithSeveralSourcesErrors(t *testing.T) {
	project, skills := standard(t)
	other := testutil.NewRepo(t)
	writeKitt(t, project, map[string]string{
		"demo":  testutil.URL(skills),
		"other": testutil.URL(other),
	})
	opt, _ := withOpts(project, false)

	err := install.Add(opt, "review@^1")

	if err == nil || !strings.Contains(err.Error(), "several sources") {
		t.Fatalf("Add err = %v", err)
	}
	assertMissing(t, skillPath(project, "review"))
}

func TestAddUnknownSourceErrors(t *testing.T) {
	project, _ := standard(t)
	opt, _ := withOpts(project, false)

	err := install.Add(opt, "nope/review@^1")

	if err == nil || !strings.Contains(err.Error(), "unknown source nope") {
		t.Fatalf("Add err = %v", err)
	}
	assertMissing(t, skillPath(project, "review"))
}

func TestAddReservedNameErrors(t *testing.T) {
	project, _ := standard(t)
	opt, _ := withOpts(project, false)

	err := install.Add(opt, "kitt-custom")

	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Add err = %v", err)
	}
	assertMissing(t, skillPath(project, "kitt-custom"))
}

func TestMissingManifestMentionsKittInit(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	cases := []struct {
		name string
		call func() error
	}{
		{name: "add", call: func() error {
			opt, _ := withOpts(project, false)
			return install.Add(opt, "review")
		}},
		{name: "restore", call: func() error {
			opt, _ := withOpts(project, false)
			return install.Restore(opt)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !strings.Contains(err.Error(), "kitt init") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
