package main

import (
	"flag"
	"os"
)

// parseFlags wires a subcommand's flag set without repeating boilerplate.
func parseFlags(name string, args []string, register func(*flag.FlagSet)) (*flag.FlagSet, []string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	register(fs)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	return fs, fs.Args()
}

// partitionArgs splits args into flag-like tokens (beginning with "-") and
// positionals, so a subcommand accepts flags before OR after its positional.
func partitionArgs(args []string) (flags, positionals []string) {
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			flags = append(flags, a)
		} else {
			positionals = append(positionals, a)
		}
	}
	return flags, positionals
}

// --- tiny ANSI color helpers (used by the CLI text renderers) ---

func green(s string) string   { return "\033[0;32m" + s + "\033[0m" }
func red(s string) string     { return "\033[0;31m" + s + "\033[0m" }
func gray(s string) string    { return "\033[0;90m" + s + "\033[0m" }
func magenta(s string) string { return "\033[0;35m" + s + "\033[0m" }
