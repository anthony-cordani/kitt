package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/anthony-cordani/kitt/internal/manifest"
)

// List prints the installed skills and the skills available in each source.
func List(opts Options) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := load(p)
	if err != nil {
		return err
	}
	out := outputOf(opts.Out)
	fmt.Fprintln(out, "Installed:")
	names := sortedNames(m.Skills)
	if len(names) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else if err := writeInstalled(out, p.skillsDir, m.Skills, names); err != nil {
		return err
	}
	aliases := make([]string, 0, len(m.Sources))
	for alias := range m.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if err := writeAvailable(out, m, alias); err != nil {
			return err
		}
	}
	return nil
}

func writeInstalled(out io.Writer, skillsDir string, skills map[string]*manifest.Skill, names []string) error {
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	for _, name := range names {
		sk := skills[name]
		if sk == nil {
			return fmt.Errorf("skill %s: nil manifest entry", name)
		}
		label, err := installedLabel(skillsDir, name, sk)
		if err != nil {
			return err
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", name, label, sk.Source)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write list: %w", err)
	}
	return nil
}

func installedLabel(skillsDir, name string, sk *manifest.Skill) (string, error) {
	if sk.Resolved == nil {
		return "not installed", nil
	}
	missing, err := skillMissing(skillsDir, name)
	if err != nil {
		return "", err
	}
	if missing {
		return "not installed", nil
	}
	return versionLabel(sk.Resolved.Version, sk.Resolved.Commit), nil
}

func skillMissing(skillsDir, name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(skillsDir, name))
	if err == nil {
		return false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, fmt.Errorf("stat skill: %w", err)
}

func writeAvailable(out io.Writer, m *manifest.Manifest, alias string) error {
	url, ok := m.Sources[alias]
	if !ok {
		return fmt.Errorf("unknown source %s", alias)
	}
	src, err := openFetch(alias, url)
	if err != nil {
		fmt.Fprintf(out, "Available in %s: unreachable (%v)\n", alias, err)
		return nil
	}
	fmt.Fprintf(out, "Available in %s:\n", alias)
	names, err := src.Skills()
	if err != nil {
		return fmt.Errorf("list source %s: %w", alias, err)
	}
	if len(names) == 0 {
		fmt.Fprintln(out, "  (none)")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	for _, name := range names {
		versions, err := src.Versions(name)
		if err != nil {
			return fmt.Errorf("list source %s: %w", alias, err)
		}
		latest := "no release"
		if len(versions) > 0 {
			latest = versions[len(versions)-1]
		}
		mark := ""
		if sk := m.Skills[name]; sk != nil && sk.Source == alias {
			mark = "installed"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", name, latest, mark)
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write list: %w", err)
	}
	return nil
}
