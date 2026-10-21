package source

import (
	"fmt"
	"os"
	"path/filepath"

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

// PublishedReleases returns the cache's published tags. Call Fetch once before
// resolving candidates: kitt never creates tags in this remote clone.
func (s *Source) PublishedReleases(name string) ([]Resolved, error) {
	versions, err := s.Versions(name)
	if err != nil {
		return nil, err
	}
	releases := make([]Resolved, 0, len(versions))
	for _, version := range versions {
		pin, err := s.pin(name, version)
		if err != nil {
			return nil, err
		}
		releases = append(releases, pin)
	}
	return releases, nil
}

// MetadataError distinguishes malformed skill content from source access errors.
type MetadataError struct{ Err error }

func (e *MetadataError) Error() string { return e.Err.Error() }
func (e *MetadataError) Unwrap() error { return e.Err }

// ReadSkillMetadata matches installation's parse-and-name validation. A missing
// description is permitted for installed skills, including legacy releases.
func ReadSkillMetadata(dir, name string) (skill.Meta, error) {
	meta, err := skill.ParseFile(filepath.Join(dir, "SKILL.md"))
	if err == nil && meta.Name != name {
		err = fmt.Errorf("SKILL.md declares name %s", meta.Name)
	}
	if err != nil {
		return skill.Meta{}, &MetadataError{Err: err}
	}
	return meta, nil
}

// Requires reads validated metadata from the supplied commit, never HEAD.
func (s *Source) Requires(name, commit string) ([]string, error) {
	meta, err := s.SkillMetadata(name, commit)
	return meta.Requires, err
}

// SkillMetadata reads frontmatter from an exact commit, preserving its locations.
func (s *Source) SkillMetadata(name, commit string) (skill.Meta, error) {
	root, err := os.MkdirTemp("", "kitt-requires-*")
	if err != nil {
		return skill.Meta{}, fmt.Errorf("stage pinned metadata: %w", err)
	}
	defer os.RemoveAll(root)
	dest := filepath.Join(root, name)
	if err := s.Extract(name, commit, dest); err != nil {
		return skill.Meta{}, err
	}
	return ReadSkillMetadata(dest, name)
}
