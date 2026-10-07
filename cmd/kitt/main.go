// Command kitt installs, pins and publishes AI agent skills per project.
package main

import (
	"fmt"
	"io"
	"os"
)

type command struct {
	name    string
	summary string
}

var commands = []command{
	{"init", "Set up AGENTS.md, RULES.md, .agents/ and kitt.toml in the current project"},
	{"install", "Install the skills pinned in kitt.toml, or add one (-g: user level)"},
	{"upgrade", "Upgrade all skills or one skill, showing what changes"},
	{"list", "List installed and available skills"},
	{"doctor", "Check skills, links and project docs for drift"},
	{"release", "Tag and push a new version of a skill (skills repository)"},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(stdout)
		return 0
	}
	for _, c := range commands {
		if c.name == args[0] {
			fmt.Fprintf(stderr, "kitt %s: not implemented yet\n", c.name)
			return 1
		}
	}
	fmt.Fprintf(stderr, "kitt: unknown command %q\n\n", args[0])
	usage(stderr)
	return 1
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "kitt - install, pin and publish AI agent skills per project.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  kitt <command> [arguments]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-8s %s\n", c.name, c.summary)
	}
}
