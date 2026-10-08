// Package scaffold sets up project files and git hooks for kitt.
package scaffold

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/source"
	"github.com/anthony-cordani/kitt/internal/templates"
)

// Options configures kitt init.
type Options struct {
	Root    string            // project root
	Sources map[string]string // alias -> URL to add to kitt.toml
	Out     io.Writer         // progress messages
}

// Init writes the missing project files, installs the git hooks, then installs the skills.
func Init(opts Options) error {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if err := checkSources(opts.Sources); err != nil {
		return err
	}
	if err := initManifest(opts); err != nil {
		return err
	}
	createdAgents, err := initAgents(opts.Root, opts.Out)
	if err != nil {
		return err
	}
	if _, err := initTemplate(opts.Root, "RULES.md", "files/RULES.md", opts.Out); err != nil {
		return err
	}
	if _, err := initTemplate(opts.Root, ".agents/docs/README.md", "files/docs/README.md", opts.Out); err != nil {
		return err
	}
	if err := initHooks(opts.Root, opts.Out); err != nil {
		return err
	}
	if err := install.Restore(install.Options{Root: opts.Root, Out: opts.Out}); err != nil {
		return fmt.Errorf("install skills: %w", err)
	}
	if createdAgents {
		fmt.Fprintln(opts.Out, "next: open your AI assistant in this project, it will run the kitt-bootstrap skill")
	}
	return nil
}

// checkSources rejects a repository URL git cannot reach, before anything is written.
func checkSources(sources map[string]string) error {
	aliases := make([]string, 0, len(sources))
	for alias := range sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if err := source.Check(sources[alias]); err != nil {
			return fmt.Errorf("source %s: %s is not a reachable git repository", alias, sources[alias])
		}
	}
	return nil
}

func initManifest(opts Options) error {
	path := filepath.Join(opts.Root, manifest.FileName)
	m, err := manifest.Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		m = &manifest.Manifest{Sources: opts.Sources}
		if err := m.Save(path); err != nil {
			return fmt.Errorf("create kitt.toml: %w", err)
		}
		fmt.Fprintln(opts.Out, "created kitt.toml")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read kitt.toml: %w", err)
	}
	aliases := make([]string, 0, len(opts.Sources))
	for alias := range opts.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	dirty := false
	for _, alias := range aliases {
		url := opts.Sources[alias]
		if existing, ok := m.Sources[alias]; ok {
			if existing != url {
				return fmt.Errorf("source %s already set to %s", alias, existing)
			}
			continue
		}
		m.Sources[alias] = url
		dirty = true
		fmt.Fprintf(opts.Out, "added source %s\n", alias)
	}
	if dirty {
		if err := m.Save(path); err != nil {
			return fmt.Errorf("update kitt.toml: %w", err)
		}
	}
	return nil
}

func initAgents(root string, out io.Writer) (bool, error) {
	path := filepath.Join(root, "AGENTS.md")
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return initTemplate(root, "AGENTS.md", "files/AGENTS.md", out)
	}
	if err != nil {
		return false, fmt.Errorf("read AGENTS.md: %w", err)
	}
	if lineIndex(string(data), templates.DocsStart) >= 0 {
		fmt.Fprintln(out, "kept AGENTS.md")
		return false, nil
	}
	section := "## Project memory\n\n" +
		"Before reading code on a specific topic, check whether a document below already explains it. When you learn something non-obvious about this project, write it down in `.agents/docs/` (format: [.agents/docs/README.md](.agents/docs/README.md)).\n\n" +
		templates.DocsStart + "\n" + templates.DocsEnd + "\n"
	updated := string(data)
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n" + section
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("update AGENTS.md: %w", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return false, fmt.Errorf("set AGENTS.md permissions: %w", err)
	}
	fmt.Fprintln(out, "updated AGENTS.md (project memory section)")
	return false, nil
}

func initTemplate(root, name, template string, out io.Writer) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(name))
	if _, err := os.Lstat(path); err == nil {
		fmt.Fprintf(out, "kept %s\n", name)
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", name, err)
	}
	data, err := templates.FS.ReadFile(template)
	if err != nil {
		return false, fmt.Errorf("read %s template: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s directory: %w", name, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false, fmt.Errorf("create %s: %w", name, err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return false, fmt.Errorf("write %s: %w", name, writeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close %s: %w", name, closeErr)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return false, fmt.Errorf("set %s permissions: %w", name, err)
	}
	fmt.Fprintf(out, "created %s\n", name)
	return true, nil
}

const (
	hookStart = "# >>> kitt (managed by kitt, do not edit)"
	hookEnd   = "# <<< kitt"
	hookBlock = hookStart + "\n" +
		"if command -v kitt >/dev/null 2>&1; then\n" +
		"  kitt install >/dev/null || echo \"kitt: install failed, run kitt install\" >&2\n" +
		"else\n" +
		"  echo \"kitt: not in PATH, skills not synced\" >&2\n" +
		"fi\n" + hookEnd + "\n"
)

func initHooks(root string, out io.Writer) error {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-path", "hooks")
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GIT_DIR" || key == "GIT_WORK_TREE" || key == "GIT_INDEX_FILE" ||
			key == "GIT_COMMON_DIR" || key == "GIT_OBJECT_DIRECTORY" || key == "GIT_ALTERNATE_OBJECT_DIRECTORIES" {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	data, err := cmd.Output()
	if err != nil {
		fmt.Fprintln(out, "warning: not a git repository, git hooks not installed")
		return nil
	}
	dir := strings.TrimSpace(string(data))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create hooks directory: %w", err)
	}
	for _, name := range []string{"post-merge", "post-checkout"} {
		if err := initHook(filepath.Join(dir, name), name, out); err != nil {
			return err
		}
	}
	return nil
}

func initHook(path, name string, out io.Writer) error {
	data, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return fmt.Errorf("read hook %s: %w", name, err)
	}
	existing := string(data)
	if !missing && strings.HasPrefix(existing, "#!") {
		line, _, _ := strings.Cut(existing, "\n")
		words := strings.Fields(strings.TrimPrefix(line, "#!"))
		interpreter := ""
		if len(words) > 0 {
			interpreter = filepath.Base(words[0])
			if interpreter == "env" && len(words) > 1 {
				words = words[1:]
				if words[0] == "-S" && len(words) > 1 {
					words = words[1:]
				}
				interpreter = words[0]
			}
		}
		if interpreter != "sh" && interpreter != "bash" && interpreter != "zsh" {
			fmt.Fprintf(out, "warning: hook %s has a non-shell interpreter, left untouched\n", name)
			return nil
		}
	}
	updated := existing
	if missing {
		updated = "#!/bin/sh\n" + hookBlock
	} else if start := lineIndex(existing, hookStart); start >= 0 {
		afterStart := lineEnd(existing, start)
		if end := lineIndex(existing[afterStart:], hookEnd); end >= 0 {
			updated = existing[:start] + hookBlock + existing[lineEnd(existing, afterStart+end):]
		} else {
			updated = appendHook(existing)
		}
	} else {
		updated = appendHook(existing)
	}
	if updated == existing {
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o755); err != nil {
		return fmt.Errorf("write hook %s: %w", name, err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return fmt.Errorf("set hook %s permissions: %w", name, err)
	}
	fmt.Fprintf(out, "installed hook %s\n", name)
	return nil
}

func appendHook(existing string) string {
	if !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	return existing + hookBlock
}

func lineIndex(text, marker string) int {
	for from := 0; from < len(text); {
		next := lineEnd(text, from)
		line := strings.TrimSuffix(strings.TrimSuffix(text[from:next], "\n"), "\r")
		if line == marker {
			return from
		}
		from = next
	}
	return -1
}

func lineEnd(text string, start int) int {
	if next := strings.IndexByte(text[start:], '\n'); next >= 0 {
		return start + next + 1
	}
	return len(text)
}
