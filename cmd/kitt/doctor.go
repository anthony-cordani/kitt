package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthony-cordani/kitt/internal/install"
)

func doctorCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt doctor [-g] [--docs] [--warn]")
		fs.PrintDefaults()
	}
	global := fs.Bool("g", false, "check user-level skills instead of the current project")
	docs := fs.Bool("docs", false, "check only project docs")
	warn := fs.Bool("warn", false, "print problems only, without failing for problems")
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
	if *global && *docs {
		fmt.Fprintln(stderr, "kitt doctor: --docs applies to a project, not to -g")
		return 1
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt doctor: %v\n", err)
		return 1
	}
	check := install.Doctor
	if *docs {
		check = install.DoctorDocs
	}
	var buf bytes.Buffer
	out := stdout
	if *warn {
		out = &buf
	}
	problems, err := check(install.Options{Global: *global, Root: root, Out: out})
	if err != nil {
		fmt.Fprintf(stderr, "kitt doctor: %v\n", err)
		return 1
	}
	if *warn {
		if problems > 0 {
			lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
			for i, line := range lines {
				if os.Getenv("GITHUB_ACTIONS") == "true" && i < len(lines)-1 {
					line = "::warning::" + line
				}
				fmt.Fprintln(stdout, line)
			}
		}
		return 0
	}
	if problems > 0 {
		return 2
	}
	return 0
}
