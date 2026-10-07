package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthony-cordani/kitt/internal/scaffold"
)

// sourceFlags collects repeated --source alias=url flags.
type sourceFlags map[string]string

func (s sourceFlags) String() string { return "" }

func (s sourceFlags) Set(v string) error {
	alias, url, ok := strings.Cut(v, "=")
	if !ok || alias == "" || url == "" {
		return errors.New("want alias=url")
	}
	s[alias] = url
	return nil
}

func initCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: kitt init [--source alias=url]...")
		fs.PrintDefaults()
	}
	sources := sourceFlags{}
	fs.Var(sources, "source", "skills repository as alias=url (repeatable)")
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
		fmt.Fprintf(stderr, "kitt init: %v\n", err)
		return 1
	}
	if len(sources) == 0 && isTerminal(os.Stdin) && !exists(filepath.Join(root, "kitt.toml")) {
		if err := askSources(bufio.NewReader(os.Stdin), stdout, sources); err != nil {
			fmt.Fprintf(stderr, "kitt init: %v\n", err)
			return 1
		}
	}
	if err := scaffold.Init(scaffold.Options{Root: root, Sources: sources, Out: stdout}); err != nil {
		fmt.Fprintf(stderr, "kitt init: %v\n", err)
		return 1
	}
	return 0
}

// askSources asks for skills repositories until an empty answer.
func askSources(in *bufio.Reader, out io.Writer, sources sourceFlags) error {
	question := "Skills repository URL (empty to skip): "
	for {
		url, err := prompt(in, out, question)
		if err != nil || url == "" {
			return err
		}
		alias, err := prompt(in, out, "Alias [default]: ")
		if err != nil {
			return err
		}
		if alias == "" {
			alias = "default"
		}
		sources[alias] = url
		question = "Another repository URL (empty to finish): "
	}
}

func prompt(in *bufio.Reader, out io.Writer, question string) (string, error) {
	fmt.Fprint(out, question)
	line, err := in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
