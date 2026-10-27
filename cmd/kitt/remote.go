package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/install"
	"github.com/anthony-cordani/kitt/internal/skill"
)

func remoteCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt remote [-g] [add <name> <url> | rm <name>]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "work on user-level sources instead of the current project")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt remote: %v\n", err)
		return 1
	}
	opts := install.Options{Global: *global, Root: root, Out: stdout}
	switch fs.Arg(0) {
	case "":
		if fs.NArg() != 0 {
			fs.Usage()
			return 1
		}
		err = install.ListSources(opts)
	case "add":
		if fs.NArg() != 3 {
			fs.Usage()
			return 1
		}
		name := fs.Arg(1)
		if nameErr := skill.ValidName(name); nameErr != nil {
			err = fmt.Errorf("invalid source name %s: %w", name, nameErr)
		} else {
			err = install.AddSources(opts, map[string]string{name: fs.Arg(2)})
		}
	case "rm":
		if fs.NArg() != 2 {
			fs.Usage()
			return 1
		}
		err = install.RemoveSource(opts, fs.Arg(1))
	default:
		fmt.Fprintf(stderr, "kitt remote: unknown subcommand %s\n", fs.Arg(0))
		fs.Usage()
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "kitt remote: %v\n", err)
		return 1
	}
	return 0
}
