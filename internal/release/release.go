// Package release tags and pushes one skill version from a skills repository.
package release

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

// Options configures a release.
type Options struct {
	Dir    string    // any directory inside the skills repository
	Remote string    // remote name, e.g. "origin"
	Out    io.Writer // progress messages
}

// Release checks that the skill can be published from the latest default branch, then tags and pushes it.
func Release(opts Options, name string) error {
	if err := skill.ValidName(name); err != nil {
		return err
	}
	if strings.HasPrefix(name, "kitt-") {
		return fmt.Errorf("skill names starting with kitt- are reserved")
	}
	root, err := repoRoot(opts.Dir)
	if err != nil {
		return err
	}
	if _, err := runGit(root, "fetch", "--quiet", "--tags", "--", opts.Remote); err != nil {
		return err
	}
	branch, err := defaultBranch(root, opts.Remote)
	if err != nil {
		return err
	}
	current, err := gitTrim(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	if current != branch {
		return fmt.Errorf("releases are made from %s, you are on %s", branch, current)
	}
	head, err := gitTrim(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	remoteHead, err := gitTrim(root, "rev-parse", "refs/remotes/"+opts.Remote+"/"+branch)
	if err != nil {
		return err
	}
	if head != remoteHead {
		return fmt.Errorf("your %s is not the latest %s/%s: pull or push first", branch, opts.Remote, branch)
	}
	status, err := runGit(root, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("working tree not clean: commit or stash first")
	}
	meta, err := skill.Validate(filepath.Join(root, "skills", name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("skill %s not found in skills/", name)
		}
		return err
	}
	if !plainVersion(meta.Version) {
		return fmt.Errorf("metadata.version of %s must be X.Y.Z (found %q)", name, meta.Version)
	}
	last, found, err := latestVersion(root, name)
	if err != nil {
		return err
	}
	if found {
		// An unchanged tree is reported before a version that was not bumped,
		// so releasing the same commit says nothing changed.
		same, err := diffQuiet(root, name+"/v"+last, "skills/"+name)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("nothing changed in skills/%s since %s/v%s", name, name, last)
		}
		if semver.Compare("v"+meta.Version, "v"+last) <= 0 {
			return fmt.Errorf("version %s is not greater than the last release %s: bump metadata.version in skills/%s/SKILL.md", meta.Version, last, name)
		}
	}
	if err := validateRequirements(root, opts.Remote, meta); err != nil {
		return &requirementError{err}
	}
	tag := name + "/v" + meta.Version
	if _, err := runGit(root, "tag", "-a", "-m", name+" "+meta.Version, tag); err != nil {
		return err
	}
	if _, err := runGit(root, "push", "--quiet", "--", opts.Remote, "refs/tags/"+tag); err != nil {
		_, _ = runGit(root, "tag", "-d", tag)
		return fmt.Errorf("push of %s refused by %s: %w", tag, opts.Remote, err)
	}
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	fmt.Fprintf(out, "released %s %s (%s)\n", name, meta.Version, tag)
	return nil
}

// requirementError leaves the command prefix to the CLI, as other release
// errors do, while preserving structured dependency errors for callers.
type requirementError struct{ error }

func (e *requirementError) Error() string {
	return strings.TrimPrefix(e.error.Error(), "kitt release: ") + "\n  no release tag created"
}

func (e *requirementError) Unwrap() error { return e.error }

func validateRequirements(root, remote string, meta skill.Meta) error {
	path := filepath.Join(root, manifest.FileName)
	scope := dependency.Scope{Command: "release", ManifestPath: path}
	var sameSource, qualified *dependency.GitSources
	var aliases map[string]string
	for i, text := range meta.Requires {
		ref, err := dependency.ParseRef(text)
		if err != nil {
			return &dependency.ValidationError{
				Location: fmt.Sprintf("%s/%s@%s metadata.requires[%d] in %s (project scope)", remote, meta.Name, meta.Version, i, filepath.Join(root, "skills", meta.Name, "SKILL.md")),
				Err:      err,
			}
		}
		bare := ref.Source == ""
		ref = ref.WithDefaultSource(remote)
		request := dependency.Requirement{Ref: ref, Requester: meta.Name, Source: remote, Version: meta.Version, Index: i}
		access := sameSource
		if bare {
			if sameSource == nil {
				url, err := releaseRemoteURL(root, remote)
				if err != nil {
					return fmt.Errorf("%s/%s@%s requires %q: %w", remote, meta.Name, meta.Version, text, err)
				}
				sameSource = dependency.NewGitSources(map[string]string{remote: url})
			}
			access = sameSource
		} else {
			if qualified == nil {
				m, err := manifest.Load(path)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("%s/%s@%s requires %q in project scope: %w", remote, meta.Name, meta.Version, text, err)
				}
				aliases = map[string]string{}
				if m != nil {
					aliases = m.Sources
				}
				qualified = dependency.NewGitSources(aliases)
			}
			if _, ok := aliases[ref.Source]; !ok {
				return &dependency.UnknownSourceError{Scope: scope, Alias: ref.Source, Requirement: request}
			}
			access = qualified
		}
		releases, err := access.Releases(ref.Source, ref.Name)
		if err != nil {
			return fmt.Errorf("%s/%s@%s requires %q in project scope: %w", remote, meta.Name, meta.Version, ref.String(), &dependency.AccessError{Source: ref.Source, Name: ref.Name, Operation: "list published releases", Err: err})
		}
		if len(releases) == 0 {
			return &dependency.NoReleasesError{Scope: scope, Source: ref.Source, Name: ref.Name, Requests: []dependency.Requirement{request}}
		}
		matched := false
		for _, release := range releases {
			ok, err := source.Satisfies(ref.Constraint, release.Version)
			if err != nil {
				return err
			}
			matched = matched || ok
		}
		if !matched {
			return &dependency.ConflictError{Scope: scope, Name: ref.Name, Sources: []string{ref.Source}, Requests: []dependency.Requirement{request}, Reason: dependency.NoMatchingRelease}
		}
	}
	return nil
}

func repoRoot(dir string) (string, error) {
	root, err := gitTrim(dir, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", errors.New("not inside a git repository")
	}
	return root, nil
}

func gitTrim(root string, args ...string) (string, error) {
	out, err := runGit(root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func defaultBranch(root, remote string) (string, error) {
	out, err := runGit(root, "ls-remote", "--symref", "--", remote, "HEAD")
	if err != nil {
		return "", err
	}
	const prefix = "ref: refs/heads/"
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		branch, _, ok := strings.Cut(strings.TrimPrefix(line, prefix), "\t")
		if !ok || branch == "" {
			break
		}
		return branch, nil
	}
	return "", fmt.Errorf("cannot find the default branch of %s", remote)
}

// plainVersion reports whether v is exactly X.Y.Z, with no prerelease or build.
func plainVersion(v string) bool {
	sv := "v" + v
	return semver.IsValid(sv) && semver.Prerelease(sv) == "" && semver.Build(sv) == "" && semver.Canonical(sv) == sv
}

func latestVersion(root, name string) (string, bool, error) {
	out, err := runGit(root, "tag", "--list", "--", name+"/v*")
	if err != nil {
		return "", false, err
	}
	prefix := name + "/v"
	best := ""
	for _, line := range strings.Split(out, "\n") {
		tag := strings.TrimSpace(line)
		if !strings.HasPrefix(tag, prefix) {
			continue
		}
		ver := strings.TrimPrefix(tag, prefix)
		if !plainVersion(ver) {
			continue
		}
		if best == "" || semver.Compare("v"+ver, "v"+best) > 0 {
			best = ver
		}
	}
	if best == "" {
		return "", false, nil
	}
	return best, true, nil
}
