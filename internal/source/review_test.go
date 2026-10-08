package source_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestFetchTracksRenamedDefaultBranch(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "", "old")
	testutil.Commit(t, repo, "old")
	s := openSource(t, "demo", repo)
	testutil.Git(t, repo, "branch", "-m", "renamed")
	testutil.WriteSkill(t, repo, "review", "", "new")
	next := testutil.Commit(t, repo, "new")
	if err := s.Fetch(); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve("review", "")
	if err != nil || r.Commit != next {
		t.Fatalf("Resolve = %+v, %v; want %s", r, err, next)
	}
	if _, err := s.Skills(); err != nil {
		t.Fatal(err)
	}
}

func TestSourceWorksWithExplicitBareRepository(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	old := testutil.Release(t, repo, "review", "1.0.0")
	next := testutil.Release(t, repo, "review", "1.1.0")
	testutil.Git(t, repo, "config", "--global", "safe.bareRepository", "explicit")
	s := openSource(t, "demo", repo)
	if err := s.Fetch(); err != nil {
		t.Fatal(err)
	}
	if !s.HasCommit(next) {
		t.Fatal("commit missing")
	}
	if _, err := s.Versions("review"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Skills(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve("review", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Diff("review", old, next); err != nil {
		t.Fatal(err)
	}
	if err := s.Extract("review", next, filepath.Join(t.TempDir(), "review")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractIgnoresGlobalAttributes(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sha := testutil.Release(t, repo, "review", "1.0.0")
	original, err := os.ReadFile(filepath.Join(repo, "skills", "review", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	attrs := filepath.Join(home, "attributes")
	testutil.WriteFile(t, attrs, "*.md text eol=crlf\n")
	testutil.Git(t, repo, "config", "--global", "core.attributesFile", attrs)
	s := openSource(t, "demo", repo)
	dest := filepath.Join(t.TempDir(), "review")
	if err := s.Extract("review", sha, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("global attributes changed archive bytes: %q", got)
	}
}

func TestVersionsRejectsShorthandAndBuildTags(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	for _, tag := range []string{"review/v2", "review/v2.1", "review/v2.1.0+build"} {
		testutil.Git(t, repo, "tag", tag)
	}
	s := openSource(t, "demo", repo)
	versions, err := s.Versions("review")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(versions, []string{"1.0.0"}) {
		t.Fatalf("versions = %v", versions)
	}
	if _, err := s.Resolve("review", ""); err != nil {
		t.Fatal(err)
	}
}
