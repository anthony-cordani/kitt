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

// KittSection tells assistants how to restore the project setup on a fresh clone.
const KittSection = KittStart + "\n" +
	"## Skills\n" +
	"\n" +
	"This project's AI skills are managed by [kitt](https://github.com/anthony-cordani/kitt): pinned in `kitt.toml`, installed in `.agents/skills/`, which git does not track. A fresh clone has none of them.\n" +
	"\n" +
	"If `kitt.toml` pins skills and `.agents/skills/` is missing, set the project up before your task:\n" +
	"\n" +
	"1. Run `kitt --version`. If kitt is not installed, tell the developer and install it only with their agreement, using the [install instructions](https://github.com/anthony-cordani/kitt#install) for their system.\n" +
	"2. Run `kitt init`. On a project that already has its files, it only adds what is missing, installs the git hooks and restores the pinned skills.\n" +
	"\n" +
	"If `kitt init` fails, report the error to the developer and carry on with the task.\n" +
	KittEnd + "\n"

// LegacySkillsLine identifies the Skills section written by older kitt versions.
const LegacySkillsLine = "Project skills live in `.agents/skills/`. They are pinned in `kitt.toml` and installed with `kitt install`."
