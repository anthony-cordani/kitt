package release

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestReleasePublishesFirstVersion(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	var buf bytes.Buffer
	if err := Release(originOpts(repo, &buf), "review"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "released review 1.0.0 (review/v1.0.0)" {
		t.Fatalf("output = %q", buf.String())
	}
	tags := testutil.Git(t, repo, "ls-remote", "--tags", "origin")
	if !strings.Contains(tags, "refs/tags/review/v1.0.0") {
		t.Fatalf("tag missing on remote:\n%s", tags)
	}
}

func TestReleaseRejectsUnchangedTree(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	if err := Release(originOpts(repo, io.Discard), "review"); err != nil {
		t.Fatal(err)
	}
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "nothing changed in skills/review since review/v1.0.0")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsVersionNotGreater(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	if err := Release(originOpts(repo, io.Discard), "review"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteSkill(t, repo, "review", "1.0.0", "review 1.0.0 edited")
	testutil.Commit(t, repo, "edit review")
	testutil.Git(t, repo, "push", "origin", "main")
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "version 1.0.0 is not greater than the last release 1.0.0: bump metadata.version in skills/review/SKILL.md")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsDirtyTree(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	testutil.WriteFile(t, filepath.Join(repo, "dirty.txt"), "dirty\n")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "working tree not clean: commit or stash first")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsMainAheadOfOrigin(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	testutil.WriteFile(t, filepath.Join(repo, "note.txt"), "local only\n")
	testutil.Commit(t, repo, "local only")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "your main is not the latest origin/main: pull or push first")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsNonDefaultBranch(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	testutil.Git(t, repo, "checkout", "-b", "feature")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "releases are made from main, you are on feature")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsNonSemverVersion(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.2")
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "review")
	requireErrPrefix(t, err, "metadata.version of review must be X.Y.Z (found \"1.2\")")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsUnknownSkill(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "missing")
	requireErrPrefix(t, err, "skill missing not found in skills/")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsDirectoryOutsideRepository(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	err := Release(Options{Dir: t.TempDir(), Remote: "origin", Out: io.Discard}, "review")
	requireErrPrefix(t, err, "not inside a git repository")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseRejectsInvalidSkillName(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "Bad")
	requireErrPrefix(t, err, "invalid skill name \"Bad\": must contain only a-z, 0-9, and hyphen")
	requireRemoteTags(t, repo, "origin", before)
}

func TestReleaseDeletesLocalTagWhenOriginRefusesPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pre-receive shell hook is skipped on Windows")
	}
	testutil.IsolateHome(t)
	repo, bare := newPushedSkillRepo(t, "origin", "1.0.0")
	script := "#!/bin/sh\nwhile read old new ref; do\n\tcase \"$ref\" in\n\trefs/tags/*)\n\t\techo rejecting tag >&2\n\t\texit 1\n\t\t;;\n\tesac\ndone\n"
	hook := filepath.Join(bare, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Release(originOpts(repo, io.Discard), "review")
	if err == nil || !strings.Contains(err.Error(), "refused by origin") {
		t.Fatalf("error = %v", err)
	}
	if tags := testutil.Git(t, repo, "tag", "--list", "review/v1.0.0"); tags != "" {
		t.Fatalf("local tag still exists: %s", tags)
	}
}

func TestReleasePublishesToUpstreamRemote(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "upstream", "1.0.0")
	var buf bytes.Buffer
	if err := Release(Options{Dir: repo, Remote: "upstream", Out: &buf}, "review"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "released review 1.0.0 (review/v1.0.0)" {
		t.Fatalf("output = %q", buf.String())
	}
	tags := testutil.Git(t, repo, "ls-remote", "--tags", "upstream")
	if !strings.Contains(tags, "refs/tags/review/v1.0.0") {
		t.Fatalf("tag missing on upstream:\n%s", tags)
	}
}

func newPushedSkillRepo(t *testing.T, remote, version string) (repo, bare string) {
	t.Helper()
	bare = t.TempDir()
	testutil.Git(t, bare, "init", "--bare", "-q", "-b", "main")
	repo = testutil.NewRepo(t)
	testutil.Git(t, repo, "config", "user.email", "test@kitt")
	testutil.Git(t, repo, "config", "user.name", "kitt test")
	testutil.Git(t, repo, "config", "commit.gpgsign", "false")
	testutil.Git(t, repo, "config", "tag.gpgsign", "false")
	testutil.WriteSkill(t, repo, "review", version, "review "+version)
	testutil.Commit(t, repo, "add review "+version)
	testutil.Git(t, repo, "remote", "add", remote, bare)
	testutil.Git(t, repo, "push", "-u", remote, "main")
	return repo, bare
}

func originOpts(repo string, out io.Writer) Options {
	return Options{Dir: repo, Remote: "origin", Out: out}
}

func remoteTags(t *testing.T, repo, remote string) string {
	t.Helper()
	return testutil.Git(t, repo, "ls-remote", "--tags", remote)
}

func requireRemoteTags(t *testing.T, repo, remote, before string) {
	t.Helper()
	after := remoteTags(t, repo, remote)
	if after != before {
		t.Fatalf("remote tags changed\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func requireErrPrefix(t *testing.T, err error, prefix string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want prefix %q", prefix)
	}
	if !strings.HasPrefix(err.Error(), prefix) {
		t.Fatalf("error %q\nwant prefix %q", err.Error(), prefix)
	}
}

func TestReleaseRejectsReservedName(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
	testutil.WriteSkill(t, repo, "kitt-custom", "1.0.0", "reserved")
	testutil.Commit(t, repo, "reserved")
	testutil.Git(t, repo, "push", "origin", "main")
	before := remoteTags(t, repo, "origin")
	err := Release(originOpts(repo, io.Discard), "kitt-custom")
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("error = %v", err)
	}
	requireRemoteTags(t, repo, "origin", before)
}
