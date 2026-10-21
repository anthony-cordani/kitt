package source

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthony-cordani/kitt/internal/skill"
)

// BranchTip pins a skill at the default branch without consulting cached tags.
// It is for unconstrained direct entries only, never dependency ranges.
func (s *Source) BranchTip(name string) (Resolved, error) {
	if err := skill.ValidName(name); err != nil {
		return Resolved{}, err
	}
	return s.resolveDegraded(name)
}

// PublishedReleases returns cached release pins whose tag objects are still
// advertised by this source's origin. Cache-only/local tags do not count.
// Call Fetch first when fresh remote candidates are needed.
func (s *Source) PublishedReleases(name string) ([]Resolved, error) {
	versions, err := s.Versions(name)
	if err != nil {
		return nil, err
	}
	out, err := runGit("-C", s.dir, "ls-remote", "--tags", "--refs", "--", "origin", "refs/tags/"+name+"/v*")
	if err != nil {
		return nil, err
	}
	advertised := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			advertised[fields[1]] = fields[0]
		}
	}
	releases := make([]Resolved, 0, len(versions))
	for _, version := range versions {
		ref := "refs/tags/" + name + "/v" + version
		object, ok := advertised[ref]
		if !ok {
			continue
		}
		cached, err := s.revParse(ref)
		if err != nil {
			return nil, err
		}
		if cached != object {
			return nil, fmt.Errorf("published tag %q changed; refresh source %q", ref, s.Alias)
		}
		pin, err := s.pin(name, version)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pin)
	}
	return releases, nil
}

// Requires reads validated metadata from the supplied commit, never HEAD.
func (s *Source) Requires(name, commit string) ([]string, error) {
	root, err := os.MkdirTemp("", "kitt-requires-*")
	if err != nil {
		return nil, fmt.Errorf("stage pinned metadata: %w", err)
	}
	defer os.RemoveAll(root)
	dest := filepath.Join(root, name)
	if err := s.Extract(name, commit, dest); err != nil {
		return nil, err
	}
	meta, err := skill.Validate(dest)
	if err != nil {
		return nil, err
	}
	return meta.Requires, nil
}
