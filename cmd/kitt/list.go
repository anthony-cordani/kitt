package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/install"
)

func listCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt list [-g]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "list user-level skills instead of the current project")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt list: %v\n", err)
		return 1
	}
	if err := install.List(install.Options{Global: *global, Root: root, Out: stdout}); err != nil {
		fmt.Fprintf(stderr, "kitt list: %v\n", err)
		return 1
	}
	return 0
}
