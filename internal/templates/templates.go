// Package templates holds the files kitt writes into a project.
package templates

import "embed"

// FS contains the project templates, rooted at "files".
//
//go:embed files
var FS embed.FS

// BootstrapSkill is the name of the embedded first-setup skill.
const BootstrapSkill = "kitt-bootstrap"

// BootstrapMarker is the AGENTS.md line that keeps the bootstrap skill installed.
const BootstrapMarker = "<!-- kitt:bootstrap -->"

// DocsStart and DocsEnd delimit the generated docs index in AGENTS.md.
const (
	DocsStart = "<!-- kitt:docs:start -->"
	DocsEnd   = "<!-- kitt:docs:end -->"
)

// ClaudeMD is the content of a CLAUDE.md managed by kitt.
const ClaudeMD = "@AGENTS.md\n"

// KittStart and KittEnd delimit the kitt section in AGENTS.md.
const (
	KittStart = "<!-- kitt:setup:start -->"
	KittEnd   = "<!-- kitt:setup:end -->"
)

// KittSection says where the project skills live and how they are installed.
const KittSection = KittStart + "\n" +
	"## Skills\n" +
	"\n" +
	"Project skills live in `.agents/skills/`. They are pinned in `kitt.toml` and installed with [kitt](https://github.com/anthony-cordani/kitt): run `kitt init` once on a fresh clone.\n" +
	KittEnd + "\n"

// LegacySkillsLine identifies the Skills section written by older kitt versions.
const LegacySkillsLine = "Project skills live in `.agents/skills/`. They are pinned in `kitt.toml` and installed with `kitt install`."
