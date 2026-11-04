package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/install"
)

func showCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt show [-g] [source/]skill[@constraint]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "use the user-level sources instead of the current project")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt show: %v\n", err)
		return 1
	}
	if err := install.Show(install.Options{Global: *global, Root: root, Out: stdout}, fs.Arg(0)); err != nil {
		fmt.Fprintf(stderr, "kitt show: %v\n", err)
		return 1
	}
	return 0
}
