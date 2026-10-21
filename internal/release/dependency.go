package release

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/anthony-cordani/kitt/internal/dependency"
	"github.com/anthony-cordani/kitt/internal/source"
)

// releaseSources checks immediate requirements only. Enumerating publications
// does not inspect their trees: the planner first selects matching candidates.
type releaseSources struct {
	root      string
	urls      map[string]string
	opened    map[string]*source.Source
	versions  map[string]string
	published map[string][]dependency.Release
}

func (s *releaseSources) Releases(alias, name string) ([]dependency.Release, error) {
	key := alias + "/" + name
	if releases, ok := s.published[key]; ok {
		return releases, nil
	}
	// Keep this local: Source.PublishedReleases also checks every cached tag's
	// skill presence. Release must check only matching candidates, and must use
	// the selected remote's advertisement, including peeled annotated tags.
	out, err := runGit(s.root, "ls-remote", "--tags", "--", s.urls[key], "refs/tags/"+name+"/v*")
	if err != nil {
		return nil, &dependency.AccessError{Source: alias, Name: name, Operation: "list published releases", Err: err}
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			refs[fields[1]] = fields[0]
		}
	}
	var releases []dependency.Release
	prefix := "refs/tags/" + name + "/v"
	for ref, object := range refs {
		version := strings.TrimPrefix(ref, prefix)
		if !strings.HasPrefix(ref, prefix) || !plainVersion(version) {
			continue
		}
		if commit, ok := refs[ref+"^{}"]; ok {
			object = commit
		}
		releases = append(releases, dependency.Release{Version: version, Commit: object})
		s.versions[key+"@"+object] = version
	}
	slices.SortFunc(releases, func(a, b dependency.Release) int {
		return semver.Compare("v"+a.Version, "v"+b.Version)
	})
	s.published[key] = releases
	return releases, nil
}

func (s *releaseSources) Requires(alias, name, commit string) ([]string, error) {
	key := alias + "/" + name
	url := s.urls[key]
	src := s.opened[url]
	if src == nil {
		var err error
		src, err = source.Open(alias, url)
		if err != nil {
			return nil, err
		}
		if err := src.Fetch(); err != nil {
			return nil, err
		}
		s.opened[url] = src
	}
	version := s.versions[key+"@"+commit]
	// Reuse source resolution for commit and skill-presence checks rather than
	// introducing another reader of commit metadata here.
	pin, err := src.Resolve(name, version)
	if err != nil {
		// A missing skill invalidates this candidate, allowing another matching
		// publication to satisfy the requirement. Preserve source error context.
		return nil, &dependency.ValidationError{Location: key + "@" + version, Err: &dependency.AccessError{
			Source: alias, Name: name, Commit: commit, Operation: "check skill presence", Err: err,
		}}
	}
	if pin.Commit != commit {
		return nil, fmt.Errorf("published tag %q changed; refresh source %q", name+"/v"+version, alias)
	}
	return nil, nil
}
