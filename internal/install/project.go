package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/templates"
	"go.yaml.in/yaml/v3"
)

func finish(p paths, names []string, prune bool, out io.Writer) error {
	previouslyInstalled := make(map[string]bool, len(p.installed.names))
	for name := range p.installed.names {
		previouslyInstalled[name] = true
	}
	if p.global {
		return syncLinks(p, names, previouslyInstalled, out)
	}
	agentsPath := filepath.Join(p.root, "AGENTS.md")
	agents, err := os.ReadFile(agentsPath)
	agentsExists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read AGENTS.md: %w", err)
	}
	active := false
	for _, line := range strings.Split(string(agents), "\n") {
		if strings.Trim(line, " \r") == templates.BootstrapMarker {
			active = true
			break
		}
	}
	if err := syncBootstrap(p, active); err != nil {
		return err
	}
	if active {
		names = append(names, templates.BootstrapSkill)
		sort.Strings(names)
	}
	// Check ownership before writeGitignore replaces the old managed block.
	if err := removeManagedClaudeMD(p.root, out); err != nil {
		return err
	}
	if agentsExists {
		if err := syncDocsIndex(p.root, agentsPath, string(agents), out); err != nil {
			return err
		}
	}
	if prune {
		if err := pruneSkills(p.installed, names, out); err != nil {
			return err
		}
	}
	if err := syncLinks(p, names, previouslyInstalled, out); err != nil {
		return err
	}
	return writeGitignore(p.root)
}

func syncBootstrap(p paths, active bool) error {
	dest := filepath.Join(p.skillsDir, templates.BootstrapSkill)
	if !active {
		if !p.installed.names[templates.BootstrapSkill] {
			return nil
		}
		if err := os.RemoveAll(dest); err != nil {
			return fmt.Errorf("remove bootstrap skill: %w", err)
		}
		return p.installed.remove(templates.BootstrapSkill)
	}
	if err := p.installed.check(templates.BootstrapSkill); err != nil {
		return err
	}
	const root = "files/kitt-bootstrap"
	err := fs.WalkDir(templates.FS, root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, root), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create bootstrap dir: %w", err)
			}
			return nil
		}
		data, err := templates.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read bootstrap template: %w", err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write bootstrap file: %w", err)
		}
		if err := os.Chmod(target, 0o644); err != nil {
			return fmt.Errorf("set bootstrap file permissions: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("install bootstrap skill: %w", err)
	}
	return p.installed.add(templates.BootstrapSkill)
}

func removeManagedClaudeMD(root string, out io.Writer) error {
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	start, end, ok := findManagedBlock(string(ignore))
	if !ok || exactLineIndex(string(ignore[start:end]), "/CLAUDE.md") < 0 {
		return nil
	}
	path := filepath.Join(root, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read CLAUDE.md: %w", err)
	}
	if string(data) != templates.ClaudeMD {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove CLAUDE.md: %w", err)
	}
	fmt.Fprintln(out, "removed CLAUDE.md (Claude Code reads AGENTS.md)")
	return nil
}

func syncDocsIndex(root, agentsPath, agents string, out io.Writer) error {
	start := exactLineIndex(agents, templates.DocsStart)
	if start < 0 {
		return nil
	}
	start = skipLine(agents, start)
	end := exactLineIndex(agents[start:], templates.DocsEnd)
	if end < 0 {
		return nil
	}
	end += start
	index, err := docsIndex(filepath.Join(root, ".agents", "docs"))
	if err != nil {
		return err
	}
	updated := agents[:start] + index + agents[end:]
	if updated == agents {
		return nil
	}
	if err := os.WriteFile(agentsPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write AGENTS.md: %w", err)
	}
	fmt.Fprintln(out, "updated AGENTS.md docs index")
	return nil
}

func exactLineIndex(s, marker string) int {
	for from := 0; from < len(s); {
		next := skipLine(s, from)
		line := strings.TrimSuffix(strings.TrimSuffix(s[from:next], "\n"), "\r")
		if line == marker {
			return from
		}
		from = next
	}
	return -1
}

// listDocs returns the project docs under dir as slash-separated relative paths,
// subfolders included. A README.md describes its folder and is not a doc.
// It uses os.ReadDir rather than filepath.WalkDir, which does not follow a
// symlinked dir. Symlinks found inside dir are not followed.
func listDocs(dir string) ([]string, error) {
	return listDocsIn(dir, "")
}

func listDocsIn(dir, prefix string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(prefix)))
	if err != nil {
		if prefix == "" && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read project docs dir: %w", err)
	}
	var docs []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			sub, err := listDocsIn(dir, prefix+name+"/")
			if err != nil {
				return nil, err
			}
			docs = append(docs, sub...)
			continue
		}
		if name == "README.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		docs = append(docs, prefix+name)
	}
	return docs, nil
}

// docLink escapes each segment of a slash-separated path, keeping the slashes.
func docLink(rel string) string {
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func docsIndex(dir string) (string, error) {
	docs, err := listDocs(dir)
	if err != nil {
		return "", err
	}
	var index strings.Builder
	for _, rel := range docs {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("read project doc %s: %w", rel, err)
		}
		title, description := docFrontmatter(string(data))
		if title == "" {
			title = strings.TrimSuffix(rel[strings.LastIndex(rel, "/")+1:], ".md")
		}
		title = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
		fmt.Fprintf(&index, "- [%s](.agents/docs/%s)", title, docLink(rel))
		if description != "" {
			fmt.Fprintf(&index, " — %s", description)
		}
		index.WriteByte('\n')
	}
	if index.Len() == 0 {
		return "- _No project docs yet._\n", nil
	}
	return index.String(), nil
}

func docFrontmatter(data string) (string, string) {
	lines := strings.Split(data, "\n")
	if strings.TrimSuffix(lines[0], "\r") != "---" {
		return "", ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSuffix(lines[i], "\r") != "---" {
			continue
		}
		var meta struct {
			Title       string `yaml:"title"`
			Description string `yaml:"description"`
		}
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:i], "\n")), &meta); err != nil {
			return "", ""
		}
		return meta.Title, meta.Description
	}
	return "", ""
}
