package install_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func mustShow(t *testing.T, root, ref string) string {
	t.Helper()
	opt, buf := withOpts(root, false)
	if err := install.Show(opt, ref); err != nil {
		t.Fatalf("Show %s: %v", ref, err)
	}
	return buf.String()
}

func showText(name, label, source, description, pinned, versions string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s)\n%s\n\n", name, label, source, description)
	if pinned != "" {
		fmt.Fprintf(&b, "Pinned here: %s\n", pinned)
	}
	fmt.Fprintf(&b, "Versions:    %s\n", versions)
	return b.String()
}

func releaseDescribed(t *testing.T, repo, name, version, description string) {
	t.Helper()
	body := "---\nname: " + name + "\ndescription: " + description + "\nmetadata:\n  version: \"" + version + "\"\n---\n" + name + " " + version + "\n"
	testutil.WriteFile(t, filepath.Join(repo, "skills", name, "SKILL.md"), body)
	testutil.Commit(t, repo, name+" "+version)
	testutil.Git(t, repo, "tag", name+"/v"+version)
}

func TestShowDefault(t *testing.T) {
	project, _ := standard(t)
	got := mustShow(t, project, "review")
	want := showText("review", "2.0.0", "demo", "Test skill review.", "", "1.0.0, 1.4.0, 2.0.0")
	if got != want {
		t.Fatalf("Show output = %q, want %q", got, want)
	}
}

func TestShowExactAndCaret(t *testing.T) {
	project, skills := newPair(t)
	for _, rel := range []struct{ version, description string }{
		{"1.0.0", "Description at 1.0.0."},
		{"1.4.0", "Description at 1.4.0."},
		{"2.0.0", "Description at 2.0.0."},
	} {
		releaseDescribed(t, skills, "review", rel.version, rel.description)
	}
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})

	const versions = "1.0.0, 1.4.0, 2.0.0"
	cases := []struct {
		ref, version, description string
	}{
		{"review@1.0.0", "1.0.0", "Description at 1.0.0."},
		{"review@^1.0", "1.4.0", "Description at 1.4.0."},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			got := mustShow(t, project, tc.ref)
			want := showText("review", tc.version, "demo", tc.description, "", versions)
			if got != want {
				t.Fatalf("Show output = %q, want %q", got, want)
			}
		})
	}
}

func TestShowPinned(t *testing.T) {
	project, _ := standard(t)
	mustAdd(t, project, "review@^1.0")
	got := mustShow(t, project, "review")
	want := showText("review", "2.0.0", "demo", "Test skill review.", "1.4.0", "1.0.0, 1.4.0, 2.0.0")
	if got != want {
		t.Fatalf("Show output = %q, want %q", got, want)
	}
}

func TestShowReadOnly(t *testing.T) {
	t.Run("no pin", func(t *testing.T) {
		project, _ := standard(t)
		before := snapshot(t, project)
		mustShow(t, project, "review")
		assertSameTree(t, before, snapshot(t, project))
		assertMissing(t, filepath.Join(project, ".agents"))
		assertMissing(t, filepath.Join(project, ".claude"))
		assertMissing(t, filepath.Join(project, ".gitignore"))
	})
	t.Run("pin", func(t *testing.T) {
		project, _ := standard(t)
		mustAdd(t, project, "review@^1.0")
		before := snapshot(t, project)
		mustShow(t, project, "review")
		assertSameTree(t, before, snapshot(t, project))
	})
}

func TestShowUnknownVersion(t *testing.T) {
	project, _ := standard(t)
	opt, _ := withOpts(project, false)
	err := install.Show(opt, "review@9.9.9")
	if err == nil || !strings.Contains(err.Error(), "no version of review matches 9.9.9") || !strings.Contains(err.Error(), "available: 1.0.0, 1.4.0, 2.0.0") {
		t.Fatalf("Show err = %v", err)
	}
}

func TestShowUnknownSkill(t *testing.T) {
	project, _ := standard(t)
	opt, _ := withOpts(project, false)
	err := install.Show(opt, "nope")
	if err == nil || !strings.Contains(err.Error(), "skill nope not found in demo") {
		t.Fatalf("Show err = %v", err)
	}
}

func TestShowTwoSources(t *testing.T) {
	project, skills := standard(t)
	other := testutil.NewRepo(t)
	release(t, other, "review", "1.0.0")
	release(t, other, "adr", "1.0.0")
	writeKitt(t, project, map[string]string{
		"demo":  testutil.URL(skills),
		"other": testutil.URL(other),
	})

	got := mustShow(t, project, "demo/review")
	want := showText("review", "2.0.0", "demo", "Test skill review.", "", "1.0.0, 1.4.0, 2.0.0")
	if got != want {
		t.Fatalf("Show demo/review = %q, want %q", got, want)
	}

	opt, _ := withOpts(project, false)
	err := install.Show(opt, "review")
	if err == nil || !strings.Contains(err.Error(), "is in several sources") {
		t.Fatalf("Show err = %v", err)
	}

	got = mustShow(t, project, "adr")
	want = showText("adr", "1.0.0", "other", "Test skill adr.", "", "1.0.0")
	if got != want {
		t.Fatalf("Show adr = %q, want %q", got, want)
	}
}

func TestShowNoRelease(t *testing.T) {
	project, skills := newPair(t)
	testutil.WriteSkill(t, skills, "draft", "", "draft")
	sha := testutil.Commit(t, skills, "draft")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})
	if len(sha) < 7 {
		t.Fatalf("commit = %q", sha)
	}
	got := mustShow(t, project, "draft")
	want := showText("draft", sha[:7], "demo", "Test skill draft.", "", "none")
	if got != want {
		t.Fatalf("Show output = %q, want %q", got, want)
	}
}

func TestShowMissingDescription(t *testing.T) {
	project, skills := newPair(t)
	skillFile := filepath.Join(skills, "skills", "review", "SKILL.md")
	testutil.WriteFile(t, skillFile, "---\nname: review\n---\n")
	testutil.Commit(t, skills, "review 1.0.0")
	testutil.Git(t, skills, "tag", "review/v1.0.0")
	// HEAD carries a description the tagged release does not.
	testutil.WriteFile(t, skillFile, "---\nname: review\ndescription: Added after the release.\n---\n")
	testutil.Commit(t, skills, "describe later")
	writeKitt(t, project, map[string]string{"demo": testutil.URL(skills)})

	got := mustShow(t, project, "review")
	want := showText("review", "1.0.0", "demo", "(no description)", "", "1.0.0")
	if got != want {
		t.Fatalf("Show output = %q, want %q", got, want)
	}
}

func TestShowOffline(t *testing.T) {
	t.Run("cached", func(t *testing.T) {
		project, skills := standard(t)
		first := mustShow(t, project, "review")
		if err := os.Rename(skills, skills+"-gone"); err != nil {
			t.Fatal(err)
		}
		opt, buf := withOpts(project, false)
		if err := install.Show(opt, "review"); err != nil {
			t.Fatalf("Show: %v\n%s", err, buf.String())
		}
		out := buf.String()
		if !strings.Contains(out, "unreachable, showing cached data") || !strings.Contains(out, first) {
			t.Fatalf("Show output = %q", out)
		}
	})
	t.Run("no cache", func(t *testing.T) {
		testutil.IsolateHome(t)
		project := testutil.NewRepo(t)
		missing := filepath.Join(t.TempDir(), "missing-repo")
		writeKitt(t, project, map[string]string{"demo": testutil.URL(missing)})
		opt, _ := withOpts(project, false)
		if err := install.Show(opt, "review"); err == nil {
			t.Fatal("Show err = nil, want an error")
		}
	})
}

func TestShowNoManifest(t *testing.T) {
	testutil.IsolateHome(t)
	project := testutil.NewRepo(t)
	opt, _ := withOpts(project, false)
	err := install.Show(opt, "review")
	if err == nil || !strings.Contains(err.Error(), "kitt.toml not found") {
		t.Fatalf("Show err = %v", err)
	}
}

func TestShowGlobal(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	release(t, repo, "review", "1.0.0")
	opt, _ := withOpts("", true)
	if err := install.AddSources(opt, map[string]string{"demo": testutil.URL(repo)}); err != nil {
		t.Fatalf("AddSources: %v", err)
	}
	opt, buf := withOpts("", true)
	if err := install.Show(opt, "review"); err != nil {
		t.Fatalf("Show: %v", err)
	}
	want := showText("review", "1.0.0", "demo", "Test skill review.", "", "1.0.0")
	if buf.String() != want {
		t.Fatalf("Show output = %q, want %q", buf.String(), want)
	}
}
