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
