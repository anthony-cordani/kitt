package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthony-cordani/kitt/internal/release"
)

func releaseCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt release [--remote name] <skill>")
		fs.PrintDefaults()
	}
	remote := fs.String("remote", "origin", "git remote to fetch from and push the tag to")
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
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "kitt release: %v\n", err)
		return 1
	}
	if err := release.Release(release.Options{Dir: dir, Remote: *remote, Out: stdout}, fs.Arg(0)); err != nil {
		fmt.Fprintf(stderr, "kitt release: %v\n", err)
		return 1
	}
	return 0
}
