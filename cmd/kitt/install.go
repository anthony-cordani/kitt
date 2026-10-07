package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/install"
)

func installCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt install [-g] [[source/]skill[@constraint]]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "install at user level instead of the current project")
	if err := fs.Parse(args); err != nil {
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
