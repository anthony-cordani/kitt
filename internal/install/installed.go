package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
)

const installedFile = ".kitt-installed"

// installedState records directories created by kitt, independently of the manifest.
type installedState struct {
	dir   string
	names map[string]bool
}

func readInstalled(p paths, m *manifest.Manifest) (*installedState, error) {
	state := &installedState{dir: p.skillsDir, names: map[string]bool{}}
	data, err := os.ReadFile(filepath.Join(p.skillsDir, installedFile))
	if err == nil {
		for _, name := range strings.Split(string(data), "\n") {
			if name == "" {
				continue
			}
			if err := skill.ValidName(name); err != nil {
				return nil, fmt.Errorf("read installed skills: %w", err)
			}
			state.names[name] = true
		}
		return state, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read installed skills: %w", err)
	}
	// A matching pin is the only evidence of ownership available to older installs.
	for _, name := range sortedNames(m.Skills) {
		sk := m.Skills[name]
		if sk == nil || sk.Resolved == nil {
			continue
		}
		dest := filepath.Join(p.skillsDir, name)
		info, err := os.Lstat(dest)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat installed skill %s: %w", name, err)
		}
		if !info.IsDir() {
			continue
		}
		match, err := hashMatches(dest, sk.Resolved.Hash)
		if err != nil {
			return nil, err
		}
		if match {
			state.names[name] = true
		}
	}
	if len(state.names) > 0 {
		if err := state.save(); err != nil {
			return nil, err
		}
	}
	return state, nil
}

func (s *installedState) check(name string) error {
	if s.names[name] {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(s.dir, name)); err == nil {
		return fmt.Errorf(".agents/skills/%s exists and was not installed by kitt: move it away or remove it", name)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat skill %s: %w", name, err)
	}
	return nil
}

func (s *installedState) add(name string) error {
	if s.names[name] {
		return nil
	}
	s.names[name] = true
	return s.save()
}

func (s *installedState) remove(name string) error {
	if !s.names[name] {
		return nil
	}
	delete(s.names, name)
	return s.save()
}

func (s *installedState) save() error {
	names := make([]string, 0, len(s.names))
	for name := range s.names {
		names = append(names, name)
	}
	sort.Strings(names)
	data := strings.Join(names, "\n")
	if len(names) > 0 {
		data += "\n"
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create skills dir: %w", err)
	}
	f, err := os.CreateTemp(s.dir, tempPrefix+"state-")
	if err != nil {
		return fmt.Errorf("write installed skills: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, writeErr := f.WriteString(data)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("write installed skills: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close installed skills: %w", closeErr)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return fmt.Errorf("set installed skills permissions: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, installedFile)); err != nil {
		return fmt.Errorf("save installed skills: %w", err)
	}
	return nil
}
