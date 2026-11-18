// Command fettle is the Fettle server, CLI and desktop backend (C-01).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Set by the release build: the version, and the computer image that this release uses.
var (
	version       = "dev"
	computerImage = ""
)

type command struct {
	summary string
	run     func(ctx context.Context, args []string) error
}

var commands = map[string]command{}

func register(name, summary string, run func(ctx context.Context, args []string) error) {
	commands[name] = command{summary: summary, run: run}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: fettle <command> [flags]")
	fmt.Fprintln(os.Stderr, "\nCommands:")
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", n, commands[n].summary)
	}
}

// errUsage prints the usage of a command.
var errUsage = errors.New("usage")

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	args := os.Args[2:]
	// Two-word commands such as "vault rekey" and "bot list".
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if _, ok := commands[name+" "+args[0]]; ok {
			name, args = name+" "+args[0], args[1:]
		}
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "fettle: unknown command %q\n\n", name)
		usage()
		os.Exit(2)
	}
	if err := c.run(context.Background(), args); err != nil {
		if errors.Is(err, errUsage) || errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "fettle:", err)
		os.Exit(1)
	}
}
