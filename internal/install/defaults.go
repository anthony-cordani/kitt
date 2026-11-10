package install

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/anthony-cordani/kitt/internal/manifest"
	"github.com/anthony-cordani/kitt/internal/skill"
)

// AddDefaults adds unpinned source defaults, warning and continuing on source errors.
func AddDefaults(opts Options) error {
	p, err := locate(opts)
	if err != nil {
		return err
	}
	m, err := manifest.Load(p.manifest)
	if err != nil {
		return err
	}
	out := outputOf(opts.Out)
	warn := func(format string, args ...any) {
		message := fmt.Sprintf(format, args...)
		fmt.Fprintf(out, "warning: %s\n", strings.Join(strings.Fields(message), " "))
	}
	aliases := make([]string, 0, len(m.Sources))
	for alias := range m.Sources {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		src, err := openFetch(alias, m.Sources[alias])
		if err != nil {
			warn("%v", err)
			continue
		}
		defaults, err := src.Defaults()
		if err != nil {
			warn("source %s defaults: %v", alias, err)
			continue
		}
		if len(defaults) == 0 {
			continue
		}
		names, err := src.Skills()
		if err != nil {
			warn("source %s skills: %v", alias, err)
			continue
		}
		for _, name := range defaults {
			if _, ok := m.Skills[name]; ok {
				continue
			}
			ref := alias + "/" + name
			if m.Defaults != nil && slices.Contains(m.Defaults.Skip, ref) {
				fmt.Fprintf(out, "skipped default %s (kitt.toml [defaults] skip)\n", ref)
				continue
			}
			if err := skill.ValidName(name); err != nil {
				warn("default %s: %v", ref, err)
				continue
			}
			if !slices.Contains(names, name) {
				warn("default %s: skill not found in source %s", ref, alias)
				continue
			}
			if err := Add(opts, ref); err != nil {
				warn("default %s: %v", ref, err)
			} else {
				fmt.Fprintf(out, "added default %s\n", ref)
			}
			m, err = manifest.Load(p.manifest)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
