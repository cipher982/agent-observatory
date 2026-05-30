package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/observatory"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
)

// runContext handles `agents context <verb>`. v1 has a single verb, `explain`,
// which prints the effective resolved context for a path. Read-only: exit 0.
func runContext(args []string) int {
	// Require the `explain` subverb (leaving room for future verbs like `diff`).
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agents context explain [path] [--json]")
		return 2
	}
	verb := args[0]
	rest := args[1:]
	switch verb {
	case "explain":
		return runContextExplain(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown context verb: %s\n", verb)
		fmt.Fprintln(os.Stderr, "usage: agents context explain [path] [--json]")
		return 2
	}
}

// runContextExplain implements `agents context explain [path] [--json]`.
// Flags may appear before OR after the path (the path is a single positional).
func runContextExplain(args []string) int {
	flags, positionals := partitionArgs(args)

	var asJSON bool
	parseFlags("context explain", flags, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "emit the raw resolver.Resolution as JSON")
	})

	// Default path is the current working directory.
	path := ""
	switch len(positionals) {
	case 0:
		path, _ = os.Getwd()
	case 1:
		path = positionals[0]
	default:
		fmt.Fprintf(os.Stderr, "context explain takes at most one path argument, got: %v\n", positionals)
		return 2
	}

	res, err := observatory.ExplainPath(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve failed: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintf(os.Stderr, "json encode failed: %v\n", err)
			return 1
		}
		return 0
	}

	printResolution(res)
	return 0
}

// printResolution renders a human-readable explanation. Provenance is the whole
// point: every active item shows the layer it came from and what it overrode.
func printResolution(res resolver.Resolution) {
	ws := res.Workspace
	if ws == "" {
		ws = "(none)"
	}
	fmt.Printf("path:      %s\n", res.Path)
	fmt.Printf("workspace: %s\n", ws)
	fmt.Println()

	// Knowledge layers, broadest-first.
	fmt.Println("knowledge layers (broadest first):")
	if len(res.Knowledge) == 0 {
		fmt.Println("  (none)")
	}
	for _, kl := range res.Knowledge {
		if kl.Exists {
			fmt.Printf("  %s [%s] %s (%d bytes)\n", green("✓"), kl.Scope, kl.Path, kl.Bytes)
		} else {
			// Missing-but-expected layer: make it loud.
			fmt.Printf("  %s [%s] %s (MISSING — expected here)\n", red("✗"), kl.Scope, kl.Path)
		}
		if kl.Label != kl.Scope.String() {
			fmt.Printf("      label: %s\n", kl.Label)
		}
	}
	fmt.Println()

	// Active skills with origin.
	activeSkills := activeItems(res.Skills)
	fmt.Printf("active skills (%d):\n", len(activeSkills))
	if len(activeSkills) == 0 {
		fmt.Println("  (none)")
	}
	for _, it := range activeSkills {
		fmt.Printf("  %s %s  (from %s%s)\n", green("•"), it.Name, it.OriginLabel, overridesNote(it))
	}
	fmt.Println()

	// Active tools with origin.
	activeTools := activeItems(res.Tools)
	fmt.Printf("active tools (%d):\n", len(activeTools))
	if len(activeTools) == 0 {
		fmt.Println("  (none)")
	}
	for _, it := range activeTools {
		fmt.Printf("  %s %s  (from %s%s)\n", green("•"), it.Name, it.OriginLabel, overridesNote(it))
	}
	fmt.Println()

	// Inactive-but-notable items: only those an overlay explicitly touched
	// (i.e. WhyInactive is something other than the bare catalog default), so we
	// don't drown the output in every disabled-by-default skill.
	notable := notableInactive(res.Skills, res.Tools)
	if len(notable) > 0 {
		fmt.Printf("inactive but notable (%d):\n", len(notable))
		for _, it := range notable {
			fmt.Printf("  %s %-22s %s — %s\n", gray("·"), it.Name, it.Kind, it.WhyInactive)
		}
	}
}

// overridesNote renders ", overrides global, workspace" when an item shadowed
// broader layers.
func overridesNote(it resolver.Item) string {
	if len(it.Overrode) == 0 {
		return ""
	}
	var labels []string
	for _, ss := range it.Overrode {
		labels = append(labels, ss.Label)
	}
	return ", overrides " + strings.Join(labels, ", ")
}

func activeItems(items []resolver.Item) []resolver.Item {
	var out []resolver.Item
	for _, it := range items {
		if it.Active {
			out = append(out, it)
		}
	}
	return out
}

// notableInactive returns inactive items that an overlay explicitly disabled —
// these are interesting (someone made a decision), unlike the long tail of
// catalog-default-off skills.
func notableInactive(groups ...[]resolver.Item) []resolver.Item {
	var out []resolver.Item
	for _, g := range groups {
		for _, it := range g {
			if it.Active {
				continue
			}
			if strings.HasPrefix(it.WhyInactive, "disabled at ") {
				out = append(out, it)
			}
		}
	}
	return out
}
