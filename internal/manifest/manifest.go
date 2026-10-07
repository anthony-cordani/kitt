// Package manifest reads and writes the kitt.toml project manifest.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileName is the project manifest name.
const FileName = "kitt.toml"

// Manifest is the content of a kitt.toml file.
type Manifest struct {
	Sources map[string]string `toml:"sources"`
	Skills  map[string]*Skill `toml:"skills"`
}

// Skill is one skill entry of the manifest.
type Skill struct {
	Source   string    `toml:"source"`
	Version  string    `toml:"version,omitempty"`  // constraint as requested
	Resolved *Resolved `toml:"resolved,omitempty"` // written by kitt only
}

// Resolved is the pinned state of a skill.
type Resolved struct {
	Version string `toml:"version"`
	Commit  string `toml:"commit"`
	Hash    string `toml:"hash"`
}

// Load reads a manifest; the maps are never nil.
func Load(path string) (*Manifest, error) {
	var m Manifest
	md, err := toml.DecodeFile(path, &m)
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, key := range undecoded {
			keys[i] = key.String()
		}
		return nil, fmt.Errorf("load manifest: unknown keys: %s", strings.Join(keys, ", "))
	}
	if m.Sources == nil {
		m.Sources = map[string]string{}
	}
	if m.Skills == nil {
		m.Skills = map[string]*Skill{}
	}
	return &m, nil
}

// Save writes the manifest atomically.
func (m *Manifest) Save(path string) error {
	if m == nil {
		return fmt.Errorf("save manifest: nil manifest")
	}
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	f, err := os.CreateTemp(dir, ".kitt-*.tmp")
	if err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	tmp := f.Name()
	encErr := toml.NewEncoder(f).Encode(m)
	closeErr := f.Close()
	if encErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("save manifest: %w", encErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("save manifest: %w", closeErr)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("save manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("save manifest: %w", err)
	}
	return nil
}

// GlobalPath returns the path of the user-level manifest.
func GlobalPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config dir: %w", err)
	}
	return filepath.Join(dir, "kitt", FileName), nil
}
