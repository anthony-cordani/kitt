package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthony-cordani/kitt/internal/install"
)

func installCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt install [-g [--source alias=url]...] [[source/]skill[@constraint]]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "install at user level instead of the current project")
	sources := sourceFlags{}
	fs.Var(sources, "source", "with -g: add a skills repository `alias=url` to the user kitt.toml, creating it if needed (repeatable)")
	// Move flags ahead of positional arguments; --source takes the next argument as its value.
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if (arg == "--source" || arg == "-source") && i+1 < len(args) {
			flags = append(flags, arg, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
		} else {
			positional = append(positional, arg)
		}
	}
	ordered := append(flags, "--")
	ordered = append(ordered, positional...)
	if err := fs.Parse(ordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt install: %v\n", err)
		return 1
	}
	opts := install.Options{Global: *global, Root: root, Out: stdout}
	if len(sources) > 0 {
		if err := install.AddSources(opts, sources); err != nil {
			fmt.Fprintf(stderr, "kitt install: %v\n", err)
			return 1
		}
	}
	switch fs.NArg() {
	case 0:
		err = install.Restore(opts)
	case 1:
		err = install.Add(opts, fs.Arg(0))
	default:
		fs.Usage()
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "kitt install: %v\n", err)
		return 1
	}
	return 0
}
