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
	claudeManaged := false
	if agentsExists {
		claudeManaged, err = syncClaudeMD(p.root, out)
		if err != nil {
			return err
		}
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
	return writeGitignore(p.root, names, claudeManaged)
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

func syncClaudeMD(root string, out io.Writer) (bool, error) {
	path := filepath.Join(root, "CLAUDE.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(path, []byte(templates.ClaudeMD), 0o644); err != nil {
			return false, fmt.Errorf("write CLAUDE.md: %w", err)
		}
		fmt.Fprintln(out, "created CLAUDE.md")
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read CLAUDE.md: %w", err)
	}
	return string(data) == templates.ClaudeMD, nil
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

func docsIndex(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read project docs dir: %w", err)
	}
	var index strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "README.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "", fmt.Errorf("read project doc %s: %w", name, err)
		}
		title, description := docFrontmatter(string(data))
		if title == "" {
			title = strings.TrimSuffix(name, ".md")
		}
		title = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`).Replace(title)
		fmt.Fprintf(&index, "- [%s](.agents/docs/%s)", title, url.PathEscape(name))
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
