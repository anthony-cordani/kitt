package source_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestResolveSelectsConstraint(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sha100 := testutil.Release(t, repo, "review", "1.0.0")
	sha140 := testutil.Release(t, repo, "review", "1.4.0")
	sha200 := testutil.Release(t, repo, "review", "2.0.0")
	s := openSource(t, "origin", repo)

	tests := []struct {
		name       string
		constraint string
		version    string
		commit     string
		wantErr    bool
		errSub     string
	}{
		{name: "empty_is_highest", constraint: "", version: "2.0.0", commit: sha200},
		{name: "caret_major", constraint: "^1", version: "1.4.0", commit: sha140},
		{name: "caret_minor", constraint: "^1.2", version: "1.4.0", commit: sha140},
		{name: "caret_above", constraint: "^1.4.1", wantErr: true},
		{name: "exact_commit", constraint: "1.0.0", version: "1.0.0", commit: sha100},
		{name: "caret_no_match", constraint: "^3", wantErr: true, errSub: "no version of review matches ^3"},
		{name: "tilde", constraint: "~1", wantErr: true},
		{name: "gte", constraint: ">=1", wantErr: true},
		{name: "two_components", constraint: "1.2", wantErr: true},
		{name: "caret_non_numeric", constraint: "^x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.Resolve("review", tt.constraint)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q) = %+v, want error", tt.constraint, got)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("Resolve(%q) error %q, want substring %q", tt.constraint, err.Error(), tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.constraint, err)
			}
			if got.Version != tt.version || got.Commit != tt.commit {
				t.Fatalf("Resolve(%q) = %+v, want version %s commit %s", tt.constraint, got, tt.version, tt.commit)
			}
		})
	}
}

func TestResolveDegradedWithoutTags(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "", "unreleased")
	head := testutil.Commit(t, repo, "unreleased review")
	s := openSource(t, "origin", repo)

	got, err := s.Resolve("review", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "" || got.Commit != head {
		t.Fatalf("Resolve empty = %+v, want version \"\" commit %s", got, head)
	}
	if _, err := s.Resolve("review", "^1"); err == nil {
		t.Fatal(`Resolve "^1" succeeded, want error`)
	}
}

func TestResolveErrorsWhenSkillMissingAtTag(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(repo, "README.md"), "no skill\n")
	testutil.Commit(t, repo, "init")
	testutil.Git(t, repo, "tag", "review/v1.0.0")
	s := openSource(t, "library", repo)

	_, err := s.Resolve("review", "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "skill review not found in library") {
		t.Fatalf("Resolve error = %v, want skill review not found in library", err)
	}
}

func TestVersionsIgnoresPrereleaseAndNonSemver(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	testutil.Git(t, repo, "tag", "review/v1.5.0-rc.1")
	testutil.Git(t, repo, "tag", "review/vfoo")
	s := openSource(t, "origin", repo)

	got, err := s.Versions("review")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.0.0"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Versions = %q, want %q", got, want)
	}
}

func TestExtractWritesSkillTreeWithoutPrefix(t *testing.T) {
	s, sha := extractFixture(t)
	dest := filepath.Join(t.TempDir(), "review")
	if err := s.Extract("review", sha, dest); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(dest, "references", "x.md")); got != "nested\n" {
		t.Fatalf("references/x.md = %q, want %q", got, "nested\n")
	}
	if skillMD := mustRead(t, filepath.Join(dest, "SKILL.md")); !strings.Contains(skillMD, "name: review") {
		t.Fatalf("SKILL.md = %q", skillMD)
	}
	if _, err := os.Lstat(filepath.Join(dest, "skills")); !os.IsNotExist(err) {
		t.Fatalf("skills prefix present at destination: %v", err)
	}
}

func TestExtractRefusesExistingDestination(t *testing.T) {
	s, sha := extractFixture(t)
	dest := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dest, "keep.txt")
	testutil.WriteFile(t, marker, "keep\n")

	if err := s.Extract("review", sha, dest); err == nil {
		t.Fatal("Extract into an existing destination succeeded")
	}
	if got := mustRead(t, marker); got != "keep\n" {
		t.Fatalf("existing destination changed: %q", got)
	}
}

func TestExtractRefusesNonHexCommit(t *testing.T) {
	s, _ := extractFixture(t)
	dest := filepath.Join(t.TempDir(), "out")
	err := s.Extract("review", "--upload-pack=x", dest)
	if err == nil || !strings.Contains(err.Error(), "invalid commit") {
		t.Fatalf("Extract error = %v, want invalid commit", err)
	}
	mustNotExist(t, dest)
}

func TestExtractRefusesInvalidSkillName(t *testing.T) {
	s, sha := extractFixture(t)
	dest := filepath.Join(t.TempDir(), "out")
	if err := s.Extract("Review", sha, dest); err == nil {
		t.Fatal("Extract accepted an invalid skill name")
	}
	mustNotExist(t, dest)
}

func TestOpenRejectsURLStartingWithDash(t *testing.T) {
	testutil.IsolateHome(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	url := "--upload-pack=touch " + marker
	if _, err := source.Open("evil", url); err == nil {
		t.Fatal("Open succeeded for a URL starting with '-'")
	}
	mustNotExist(t, marker)
}

func TestHasCommit(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sha := testutil.Release(t, repo, "review", "1.0.0")
	s := openSource(t, "origin", repo)

	if !s.HasCommit(sha) {
		t.Fatalf("HasCommit(%s) = false, want true", sha)
	}
	unknown := "a" + sha[1:]
	if sha[0] == 'a' {
		unknown = "b" + sha[1:]
	}
	if s.HasCommit(unknown) {
		t.Fatalf("HasCommit(%s) = true for an unknown SHA", unknown)
	}
	for _, bad := range []string{"HEAD", "--upload-pack=x", "not-a-sha"} {
		if s.HasCommit(bad) {
			t.Fatalf("HasCommit(%q) = true, want false", bad)
		}
	}
}

func TestFetchPicksUpReleaseTaggedAfterOpen(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.Release(t, repo, "review", "1.0.0")
	s := openSource(t, "origin", repo)

	before, err := s.Resolve("review", "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != "1.0.0" {
		t.Fatalf("before new release: version %q, want 1.0.0", before.Version)
	}

	sha := testutil.Release(t, repo, "review", "1.1.0")
	cached, err := s.Resolve("review", "")
	if err != nil {
		t.Fatal(err)
	}
	if cached.Version != "1.0.0" {
		t.Fatalf("before Fetch: version %q, want cached 1.0.0", cached.Version)
	}
	if err := s.Fetch(); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	after, err := s.Resolve("review", "")
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != "1.1.0" || after.Commit != sha {
		t.Fatalf("after Fetch = %+v, want version 1.1.0 commit %s", after, sha)
	}
}

func TestSkillsListsSkillDirectoriesSorted(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "zeta", "1.0.0", "z")
	testutil.WriteSkill(t, repo, "alpha", "1.0.0", "a")
	testutil.WriteSkill(t, repo, "mid", "1.0.0", "m")
	testutil.WriteFile(t, filepath.Join(repo, "skills", "notes", "README.md"), "no skill file\n")
	testutil.Commit(t, repo, "skills")
	s := openSource(t, "origin", repo)

	got, err := s.Skills()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("Skills = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Skills = %q, want %q", got, want)
		}
	}
}

func TestDiffContainsChangedLine(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	from := testutil.Release(t, repo, "review", "1.0.0")
	to := testutil.Release(t, repo, "review", "1.1.0")
	s := openSource(t, "origin", repo)

	diff, err := s.Diff("review", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+review 1.1.0") {
		t.Fatalf("diff missing changed line:\n%s", diff)
	}
}

func TestDiffRefusesNonHexCommit(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sha := testutil.Release(t, repo, "review", "1.0.0")
	s := openSource(t, "origin", repo)

	for _, args := range [][2]string{{"--upload-pack=x", sha}, {sha, "--upload-pack=x"}} {
		_, err := s.Diff("review", args[0], args[1])
		if err == nil || !strings.Contains(err.Error(), "invalid commit") {
			t.Fatalf("Diff(%q, %q) error = %v, want invalid commit", args[0], args[1], err)
		}
	}
}

func TestCheckAcceptsLocalRepository(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(repo, "README.md"), "ok\n")
	testutil.Commit(t, repo, "init")
	if err := source.Check(testutil.URL(repo)); err != nil {
		t.Fatalf("Check(local) = %v", err)
	}
}

func TestCheckRejectsMissingPath(t *testing.T) {
	testutil.IsolateHome(t)
	missing := filepath.Join(t.TempDir(), "missing")
	if err := source.Check(testutil.URL(missing)); err == nil {
		t.Fatal("Check(missing) succeeded")
	}
}

func TestLeakedGitDirDoesNotRedirectOpen(t *testing.T) {
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	sha := testutil.Release(t, repo, "review", "1.2.0")

	other := testutil.NewRepo(t)
	testutil.WriteFile(t, filepath.Join(other, "README.md"), "other\n")
	testutil.Commit(t, other, "other")

	// IsolateHome clears GIT_DIR. Set it afterwards, the way a hook leaks it.
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))

	s := openSource(t, "origin", repo)
	got, err := s.Resolve("review", "1.2.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Version != "1.2.0" || got.Commit != sha {
		t.Fatalf("Resolve = %+v, want version 1.2.0 commit %s", got, sha)
	}
}

func openSource(t *testing.T, alias, repo string) *source.Source {
	t.Helper()
	s, err := source.Open(alias, testutil.URL(repo))
	if err != nil {
		t.Fatalf("Open(%s): %v", alias, err)
	}
	return s
}

func extractFixture(t *testing.T) (*source.Source, string) {
	t.Helper()
	testutil.IsolateHome(t)
	repo := testutil.NewRepo(t)
	testutil.WriteSkill(t, repo, "review", "1.0.0", "hello")
	testutil.WriteFile(t, filepath.Join(repo, "skills", "review", "references", "x.md"), "nested\n")
	sha := testutil.Commit(t, repo, "review")
	return openSource(t, "origin", repo), sha
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat: %v", path, err)
	}
}
