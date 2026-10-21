package dependency

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/anthony-cordani/kitt/internal/skill"
	"github.com/anthony-cordani/kitt/internal/source"
)

// GitSources implements SourceAccess and TipAccess through kitt's source cache.
// Exact cached commits require no fetch. Successful refreshes are reused per plan.
// It is intentionally separate from BuildPlan, which performs no Git operations.
type GitSources struct {
	urls    map[string]string
	opened  map[string]*source.Source
	fetched map[string]bool
}

// NewGitSources copies the selected scope's aliases. Construction performs no IO.
func NewGitSources(aliases map[string]string) *GitSources {
	return &GitSources{urls: maps.Clone(aliases), opened: make(map[string]*source.Source), fetched: make(map[string]bool)}
}

func (g *GitSources) open(alias string) (*source.Source, error) {
	if s, ok := g.opened[alias]; ok {
		return s, nil
	}
	url, ok := g.urls[alias]
	if !ok {
		return nil, fmt.Errorf("unknown source alias %q", alias)
	}
	s, err := source.Open(alias, url)
	if err != nil {
		return nil, err
	}
	g.opened[alias] = s
	return s, nil
}

// Releases returns only tags advertised by the configured repository.
func (g *GitSources) Releases(alias, name string) ([]Release, error) {
	s, err := g.refresh(alias)
	if err != nil {
		return nil, err
	}
	pins, err := s.PublishedReleases(name)
	if err != nil {
		return nil, err
	}
	releases := make([]Release, len(pins))
	for i, pin := range pins {
		releases[i] = Release{Version: pin.Version, Commit: pin.Commit}
	}
	return releases, nil
}

// Requires reads frontmatter at the selected commit.
func (g *GitSources) Requires(alias, name, commit string) ([]string, error) {
	s, err := g.open(alias)
	if err != nil {
		return nil, err
	}
	if !s.HasCommit(commit) {
		if err := s.Fetch(); err != nil {
			return nil, err
		}
	}
	root, err := os.MkdirTemp("", "kitt-requires-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	dest := filepath.Join(root, name)
	if err := s.Extract(name, commit, dest); err != nil {
		return nil, err
	}
	meta, err := skill.Validate(dest)
	if err != nil {
		return nil, &ValidationError{Location: name + "/SKILL.md", Err: err}
	}
	return meta.Requires, nil
}

func (g *GitSources) refresh(alias string) (*source.Source, error) {
	s, err := g.open(alias)
	if err != nil {
		return nil, err
	}
	if !g.fetched[alias] {
		if err := s.Fetch(); err != nil {
			return nil, err
		}
		g.fetched[alias] = true
	}
	return s, nil
}

// Tip preserves branch-tip support for unconstrained direct installs.
func (g *GitSources) Tip(alias, name string) (Release, error) {
	s, err := g.refresh(alias)
	if err != nil {
		return Release{}, err
	}
	pin, err := s.BranchTip(name)
	return Release{Version: pin.Version, Commit: pin.Commit}, err
}
