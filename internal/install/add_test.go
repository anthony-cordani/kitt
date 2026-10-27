package install_test

import (
	"errors"
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

func TestAddBareNameSeveralSources(t *testing.T) {
	project, skills := standard(t)
	other := testutil.NewRepo(t)
	release(t, other, "review", "1.0.0")
	writeKitt(t, project, map[string]string{
		"demo":  testutil.URL(skills),
		"other": testutil.URL(other),
	})
	before := snapshot(t, project)
	opt, _ := withOpts(project, false)

	err := install.Add(opt, "review@^1")

	if err == nil || err.Error() != "skill review is in several sources (demo, other): use <source>/review" {
		t.Fatalf("Add err = %v", err)
	}
	assertMissing(t, skillPath(project, "review"))
	assertSameTree(t, before, snapshot(t, project))
}

func TestAddBareNameUniqueAcrossSources(t *testing.T) {
	for _, alias := range []string{"alpha", "zulu"} {
		t.Run(alias, func(t *testing.T) {
			project, skills := standard(t)
			other := testutil.NewRepo(t)
			release(t, other, "adr", "1.0.0")
			writeKitt(t, project, map[string]string{
				"demo": testutil.URL(skills),
				alias:  testutil.URL(other),
			})
			out := mustAdd(t, project, "adr@^1")
			if out != "added adr 1.0.0\n" {
				t.Fatalf("output = %q", out)
			}
			sk := skillEntry(t, project, "adr")
			if sk.Source != alias {
				t.Fatalf("source = %q, want %q", sk.Source, alias)
			}
			assertResolved(t, sk, "^1", "1.0.0")
			body := readText(t, filepath.Join(skillPath(project, "adr"), "SKILL.md"))
			if !strings.Contains(body, "\nadr 1.0.0\n") {
				t.Fatalf("installed skill =\n%s", body)
			}
		})
	}
}

func TestAddBareNameNotFound(t *testing.T) {
	project, skills := standard(t)
	other := testutil.NewRepo(t)
	release(t, other, "adr", "1.0.0")
	writeKitt(t, project, map[string]string{
		"demo":  testutil.URL(skills),
		"other": testutil.URL(other),
	})
	before := snapshot(t, project)
	opt, _ := withOpts(project, false)
	err := install.Add(opt, "missing")
	if err == nil || err.Error() != "skill missing not found in any source (demo, other)" {
		t.Fatalf("Add err = %v", err)
	}
	assertSameTree(t, before, snapshot(t, project))
}

func TestAddBareNameUnreachableSource(t *testing.T) {
	for _, failure := range []string{"open", "list"} {
		t.Run(failure, func(t *testing.T) {
			project, skills := standard(t)
			other := testutil.NewRepo(t)
			if failure == "open" {
				other = filepath.Join(other, "missing")
			}
			writeKitt(t, project, map[string]string{
				"demo":  testutil.URL(skills),
				"other": testutil.URL(other),
			})
			before := snapshot(t, project)
			opt, _ := withOpts(project, false)
			err := install.Add(opt, "review")
			if err == nil || !strings.HasPrefix(err.Error(), "source other unreachable: use <source>/<skill>: ") || errors.Unwrap(err) == nil {
				t.Fatalf("Add err = %v, want wrapped source error", err)
			}
			assertSameTree(t, before, snapshot(t, project))
		})
	}
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
