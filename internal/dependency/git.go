package dependency

import (
	"fmt"
	"maps"

	"github.com/anthony-cordani/kitt/internal/source"
)

// GitSources implements SourceAccess and TipAccess through kitt's source cache.
// It opens/fetches each alias at most once. Use a fresh instance for each plan.
// It is intentionally separate from BuildPlan, which performs no Git operations.
type GitSources struct {
	urls     map[string]string
	opened   map[string]*source.Source
	failures map[string]error
}

// NewGitSources copies the selected scope's aliases. Construction performs no IO.
func NewGitSources(aliases map[string]string) *GitSources {
	return &GitSources{urls: maps.Clone(aliases), opened: make(map[string]*source.Source), failures: make(map[string]error)}
}

func (g *GitSources) open(alias string) (*source.Source, error) {
	if s, ok := g.opened[alias]; ok {
		return s, nil
	}
	if err, ok := g.failures[alias]; ok {
		return nil, err
	}
	url, ok := g.urls[alias]
	if !ok {
		return nil, fmt.Errorf("unknown source alias %q", alias)
	}
	s, err := source.Open(alias, url)
	if err == nil {
		err = s.Fetch()
	}
	if err != nil {
		g.failures[alias] = err
		return nil, err
	}
	g.opened[alias] = s
	return s, nil
}

// Releases returns only tags advertised by the configured repository.
func (g *GitSources) Releases(alias, name string) ([]Release, error) {
	s, err := g.open(alias)
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
	return s.Requires(name, commit)
}

// Tip preserves branch-tip support for unconstrained direct installs.
func (g *GitSources) Tip(alias, name string) (Release, error) {
	s, err := g.open(alias)
	if err != nil {
		return Release{}, err
	}
	pin, err := s.BranchTip(name)
	return Release{Version: pin.Version, Commit: pin.Commit}, err
}
