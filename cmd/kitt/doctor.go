package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/install"
)

func doctorCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt doctor [-g]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "check user-level skills instead of the current project")
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
		fmt.Fprintf(stderr, "kitt doctor: %v\n", err)
		return 1
	}
	problems, err := install.Doctor(install.Options{Global: *global, Root: root, Out: stdout})
	if err != nil {
		fmt.Fprintf(stderr, "kitt doctor: %v\n", err)
		return 1
	}
	if problems > 0 {
		return 2
	}
	return 0
}
