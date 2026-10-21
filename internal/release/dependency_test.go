package release

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/testutil"
)

func TestReleaseValidRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, remote, ref string
		cross             bool
	}{
		{name: "same source caret", remote: "origin", ref: "helper@^1.0.0"},
		{name: "same source exact", remote: "origin", ref: "helper@1.2.3"},
		{name: "selected non-origin remote", remote: "upstream", ref: "helper@^1"},
		{name: "cross source", remote: "origin", ref: "team/helper@^1", cross: true},
		{name: "alias matches remote name", remote: "team", ref: "team/helper@1.2.3", cross: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo, bare := newPushedSkillRepo(t, tc.remote, "1.0.0")
			if tc.cross {
				other := testutil.NewRepo(t)
				testutil.Release(t, other, "helper", "1.2.3")
				writeReleaseSources(t, repo, map[string]string{"team": testutil.URL(other)})
			} else {
				// Only immediate requirements are checked, never helper's graph.
				testutil.WriteSkillRequires(t, repo, "helper", "1.2.3", []string{"not-yet-released@^1"})
				testutil.Commit(t, repo, "helper release")
				testutil.Git(t, repo, "tag", "-a", "helper/v1.2.3", "-m", "helper release")
				testutil.Git(t, repo, "push", tc.remote, "refs/tags/helper/v1.2.3")
				// A bare reference must ignore even a configured alias named
				// after the release remote. No manifest is needed otherwise.
				if tc.remote == "upstream" {
					other := testutil.NewRepo(t)
					testutil.WriteFile(t, filepath.Join(other, "README"), "no skills")
					testutil.Commit(t, other, "empty source")
					testutil.Git(t, repo, "remote", "add", "origin", testutil.URL(other))
					writeReleaseSources(t, repo, map[string]string{"upstream": testutil.URL(other)})
				}
			}
			pushRequirements(t, repo, tc.remote, []string{tc.ref})
			// Locate cross-source configuration at the repository root even
			// when release is invoked from the skill directory.
			if err := Release(Options{Dir: filepath.Join(repo, "skills", "review"), Remote: tc.remote}, "review"); err != nil {
				t.Fatal(err)
			}
			if tags := testutil.Git(t, bare, "tag", "--list", "review/v1.0.0"); tags != "review/v1.0.0" {
				t.Fatalf("release tag missing: %q", tags)
			}
		})
	}
}

func TestReleaseRequirementFailuresLeaveTagsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, ref, want string
		setup           func(t *testing.T, repo string)
		kind            string
	}{
		{name: "missing constraint", ref: "helper", want: `invalid dependency reference "helper": missing @constraint`, kind: "validation"},
		{name: "unsupported constraint", ref: "helper@>=1", want: `invalid dependency reference "helper@>=1"`, kind: "validation"},
		{name: "unknown alias", ref: "other/helper@^1", want: `unknown source alias "other"`, kind: "alias", setup: func(t *testing.T, repo string) {
			writeReleaseSources(t, repo, map[string]string{"team": testutil.URL(repo)})
		}},
		{name: "no repository manifest or global fallback", ref: "other/helper@^1", want: `unknown source alias "other"`, kind: "alias", setup: func(t *testing.T, repo string) {
			global, err := manifest.GlobalPath()
			if err != nil {
				t.Fatal(err)
			}
			m := &manifest.Manifest{Sources: map[string]string{"other": testutil.URL(repo)}}
			if err := m.Save(global); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no published release", ref: "helper@^1", want: "helper has no published releases; a branch-tip pin cannot satisfy this range", kind: "no releases", setup: func(t *testing.T, repo string) {
			testutil.WriteSkillRequires(t, repo, "helper", "1.2.3", nil)
		}},
		{name: "no matching release", ref: "helper@^2", want: "ranges overlap, but no published release matches", kind: "conflict", setup: func(t *testing.T, repo string) {
			testutil.Release(t, repo, "helper", "1.2.3")
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.2.3")
		}},
		{name: "local-only tag", ref: "helper@^1", want: "helper has no published releases", kind: "no releases", setup: func(t *testing.T, repo string) {
			testutil.Release(t, repo, "helper", "1.2.3")
		}},
		{name: "local matching tag and published nonmatching tag", ref: "helper@^2", want: "ranges overlap, but no published release matches", kind: "conflict", setup: func(t *testing.T, repo string) {
			testutil.Release(t, repo, "helper", "1.2.3")
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.2.3")
			testutil.Release(t, repo, "helper", "2.0.0")
		}},
		{name: "skill absent at release", ref: "helper@^1", want: "skill helper not found", kind: "access", setup: func(t *testing.T, repo string) {
			testutil.Git(t, repo, "tag", "helper/v1.2.3")
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.2.3")
			// Existing on HEAD is insufficient: it must exist at the tag.
			testutil.WriteSkillRequires(t, repo, "helper", "1.2.3", nil)
		}},
		{name: "cross source git failure", ref: "team/helper@^1", want: "list published releases team/helper", kind: "access", setup: func(t *testing.T, repo string) {
			writeReleaseSources(t, repo, map[string]string{"team": testutil.URL(filepath.Join(t.TempDir(), "missing.git"))})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
			if tc.setup != nil {
				tc.setup(t, repo)
			}
			// Check every entry: a valid first requirement cannot hide a
			// later invalid one.
			testutil.Release(t, repo, "valid", "1.0.0")
			testutil.Git(t, repo, "push", "origin", "refs/tags/valid/v1.0.0")
			pushRequirements(t, repo, "origin", []string{"valid@1.0.0", tc.ref})
			err := requireReleaseFailureWithoutTags(t, repo, "origin", tc.want)
			if strings.Contains(err.Error(), "kitt release:") {
				t.Fatalf("command prefix should be supplied by CLI: %v", err)
			}
			switch tc.kind {
			case "validation":
				var e *dependency.ValidationError
				if !errors.As(err, &e) || !strings.Contains(err.Error(), "metadata.requires[1]") {
					t.Fatalf("missing structured parse context: %v", err)
				}
			case "alias":
				var e *dependency.UnknownSourceError
				root := testutil.Git(t, repo, "rev-parse", "--show-toplevel")
				if !errors.As(err, &e) || e.Scope.ManifestPath != filepath.Join(root, manifest.FileName) || !strings.Contains(err.Error(), `kitt init --source "other=<repository-url>"`) {
					t.Fatalf("missing alias recovery context: %v", err)
				}
			case "no releases":
				var e *dependency.NoReleasesError
				if !errors.As(err, &e) {
					t.Fatalf("missing no-release error: %v", err)
				}
			case "conflict":
				var e *dependency.ConflictError
				if !errors.As(err, &e) || e.Reason != dependency.NoMatchingRelease {
					t.Fatalf("missing no-match error: %v", err)
				}
			case "access":
				var e *dependency.AccessError
				if !errors.As(err, &e) {
					t.Fatalf("missing source access error: %v", err)
				}
			}
			if !strings.Contains(err.Error(), "origin/review@1.0.0") {
				t.Fatalf("missing requester context: %v", err)
			}
		})
	}
}

func TestReleaseRequirementsUseSelectedRemotePublication(t *testing.T) {
	testutil.IsolateHome(t)
	repo, _ := newPushedSkillRepo(t, "upstream", "1.0.0")
	testutil.Release(t, repo, "helper", "1.0.0")
	// origin publishes the tag, but the selected upstream does not.
	origin := testutil.NewRepo(t)
	testutil.Git(t, repo, "remote", "add", "origin", testutil.URL(origin))
	testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.0.0")
	pushRequirements(t, repo, "upstream", []string{"helper@^1"})
	originBefore := remoteTags(t, repo, "origin")
	requireReleaseFailureWithoutTags(t, repo, "upstream", "helper has no published releases")
	requireRemoteTags(t, repo, "origin", originBefore)
}

func writeReleaseSources(t *testing.T, repo string, aliases map[string]string) {
	t.Helper()
	m := &manifest.Manifest{Sources: aliases}
	if err := m.Save(filepath.Join(repo, manifest.FileName)); err != nil {
		t.Fatal(err)
	}
}

func pushRequirements(t *testing.T, repo, remote string, refs []string) {
	t.Helper()
	testutil.WriteSkillRequires(t, repo, "review", "1.0.0", refs)
	testutil.Commit(t, repo, "declare release requirements")
	testutil.Git(t, repo, "push", remote, "main")
}

func requireReleaseFailureWithoutTags(t *testing.T, repo, remote, want string) error {
	t.Helper()
	localBefore := testutil.Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags/")
	remoteBefore := remoteTags(t, repo, remote)
	err := Release(Options{Dir: repo, Remote: remote, Out: io.Discard}, "review")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if !strings.Contains(err.Error(), "no release tag created") {
		t.Fatalf("missing no-tag assurance: %v", err)
	}
	localAfter := testutil.Git(t, repo, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags/")
	if localAfter != localBefore {
		t.Fatalf("local tags changed\nbefore:\n%s\nafter:\n%s", localBefore, localAfter)
	}
	requireRemoteTags(t, repo, remote, remoteBefore)
	return err
}

func TestReleaseRequirementSetFailuresLeaveTagsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, version, want string
		refs                []string
		reason              dependency.ConflictReason
	}{
		{name: "disjoint ranges", version: "1.1.0", refs: []string{"helper@^1", "helper@^2"}, want: "incompatible requirements for origin/helper", reason: dependency.EmptyIntersection},
		{name: "source collision", version: "1.1.0", refs: []string{"helper@^1", "team/helper@^1"}, want: `conflicting sources: "origin/helper", "team/helper"`, reason: dependency.SourceCollision},
		{name: "self requirement", version: "1.1.0", refs: []string{"review@^1"}, want: "dependency cycle: origin/review@1.1.0 -> origin/review@1.1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo, _ := newPushedSkillRepo(t, "origin", "1.0.0")
			testutil.Git(t, repo, "tag", "review/v1.0.0")
			testutil.Git(t, repo, "push", "origin", "refs/tags/review/v1.0.0")
			testutil.Release(t, repo, "helper", "1.2.3")
			testutil.Release(t, repo, "helper", "2.0.0")
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.2.3", "refs/tags/helper/v2.0.0")
			other := testutil.NewRepo(t)
			testutil.Release(t, other, "helper", "1.2.3")
			writeReleaseSources(t, repo, map[string]string{"team": testutil.URL(other)})
			testutil.WriteSkillRequires(t, repo, "review", tc.version, tc.refs)
			testutil.Commit(t, repo, "declare conflicting requirements")
			testutil.Git(t, repo, "push", "origin", "main")
			err := requireReleaseFailureWithoutTags(t, repo, "origin", tc.want)
			if tc.reason != "" {
				var conflict *dependency.ConflictError
				if !errors.As(err, &conflict) || conflict.Reason != tc.reason || len(conflict.Requests) != 2 {
					t.Fatalf("missing planner conflict and both requesters: %v", err)
				}
			} else {
				var cycle *dependency.CycleError
				if !errors.As(err, &cycle) {
					t.Fatalf("missing planner cycle: %v", err)
				}
			}
		})
	}
}

func TestReleaseSkillPresenceOnlyOnMatchingReleases(t *testing.T) {
	for _, absentVersion := range []string{"0.1.0", "1.3.0"} {
		t.Run(absentVersion, func(t *testing.T) {
			testutil.IsolateHome(t)
			repo, bare := newPushedSkillRepo(t, "origin", "1.0.0")
			// The advertised tag predates the helper's directory. Even a
			// matching bad tag must not hide a valid matching release.
			testutil.Git(t, repo, "tag", "helper/v"+absentVersion)
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v"+absentVersion)
			testutil.Release(t, repo, "helper", "1.2.3")
			testutil.Git(t, repo, "push", "origin", "refs/tags/helper/v1.2.3")
			pushRequirements(t, repo, "origin", []string{"helper@^1", "helper@^1.2"})
			if err := Release(originOpts(repo, io.Discard), "review"); err != nil {
				t.Fatal(err)
			}
			if tags := testutil.Git(t, bare, "tag", "--list", "review/v1.0.0"); tags != "review/v1.0.0" {
				t.Fatalf("release tag missing: %q", tags)
			}
		})
	}
}

func TestReleaseRelativeSourceAliasFromSubdirectory(t *testing.T) {
	testutil.IsolateHome(t)
	repo, bare := newPushedSkillRepo(t, "origin", "1.0.0")
	other := testutil.NewRepo(t)
	testutil.Release(t, other, "helper", "1.2.3")
	relative, err := filepath.Rel(repo, other)
	if err != nil {
		t.Fatal(err)
	}
	writeReleaseSources(t, repo, map[string]string{"team": relative})
	pushRequirements(t, repo, "origin", []string{"team/helper@^1"})
	t.Chdir(filepath.Join(repo, "skills", "review"))
	if err := Release(originOpts(".", io.Discard), "review"); err != nil {
		t.Fatal(err)
	}
	if tags := testutil.Git(t, bare, "tag", "--list", "review/v1.0.0"); tags != "review/v1.0.0" {
		t.Fatalf("release tag missing: %q", tags)
	}
}
