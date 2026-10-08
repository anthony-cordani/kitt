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

func upgradeCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt upgrade [-g] [--major] [skill]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "upgrade user-level skills instead of the current project")
	major := fs.Bool("major", false, "allow a new major version")
	// These commands have only boolean flags; move them ahead of positional arguments.
	var flags, positional []string
	for i, arg := range args {
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
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
	if fs.NArg() > 1 {
		fs.Usage()
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt upgrade: %v\n", err)
		return 1
	}
	opts := install.Options{Global: *global, Root: root, Out: stdout}
	if err := install.Upgrade(opts, fs.Arg(0), *major); err != nil {
		fmt.Fprintf(stderr, "kitt upgrade: %v\n", err)
		return 1
	}
	return 0
}
