package install

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
)

// ListSources prints the configured sources in alias order.
func ListSources(opts Options) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := manifest.Load(p.manifest)
	if errors.Is(err, fs.ErrNotExist) {
		if p.global {
			return nil
		}
		return fmt.Errorf("no kitt.toml here")
	}
	if err != nil {
		return err
	}
	aliases := make([]string, 0, len(m.Sources))
	for alias := range m.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		fmt.Fprintf(outputOf(opts.Out), "%s\t%s\n", alias, m.Sources[alias])
	}
	return nil
}

// RemoveSource removes an unused source from the target manifest.
func RemoveSource(opts Options, alias string) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := manifest.Load(p.manifest)
	if errors.Is(err, fs.ErrNotExist) {
		if p.global {
			return fmt.Errorf("no user kitt.toml")
		}
		return fmt.Errorf("no kitt.toml here")
	}
	if err != nil {
		return err
	}
	if _, ok := m.Sources[alias]; !ok {
		return fmt.Errorf("unknown source %s", alias)
	}
	var users []string
	for _, name := range sortedNames(m.Skills) {
		if s := m.Skills[name]; s != nil && s.Source == alias {
			users = append(users, name)
		}
	}
	if len(users) > 0 {
		return fmt.Errorf("source %s is used by %s: remove or move those skills first", alias, strings.Join(users, ", "))
	}
	delete(m.Sources, alias)
	if err := m.Save(p.manifest); err != nil {
		return err
	}
	fmt.Fprintf(outputOf(opts.Out), "removed source %s\n", alias)
	return nil
}
