package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/observatory"
)

// runSessions handles `agents sessions [--json] [--limit N]`: discovered agent
// sessions joined with their resolved context and fact-level evidence marks.
func runSessions(args []string) int {
	var (
		asJSON bool
		limit  int
	)
	parseFlags("sessions", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "emit []observatory.SessionView as JSON")
		fs.IntVar(&limit, "limit", 25, "cap to N most-recent sessions")
	})

	views, err := observatory.LiveSessions(limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover failed: %v\n", err)
		return 1
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(views); err != nil {
			fmt.Fprintf(os.Stderr, "json encode failed: %v\n", err)
			return 1
		}
		return 0
	}
	printSessions(views)
	return 0
}

func printSessions(views []observatory.SessionView) {
	if len(views) == 0 {
		fmt.Println("no sessions found")
		return
	}
	fmt.Printf("%-12s %-12s %-30s %-17s %-9s %s\n",
		"RUNTIME", "WORKSPACE", "CWD", "LAST ACTIVITY", "LEVEL", "MARKS")
	for _, v := range views {
		ws := v.Workspace
		if ws == "" {
			ws = "-"
		}
		fmt.Printf("%-12s %-12s %-30s %-17s %-9s %s\n",
			v.Session.Runtime,
			truncate(ws, 12),
			truncate(shortenHome(v.Session.CWD), 30),
			v.Session.LastActivity.Format("01-02 15:04"),
			strings.ToUpper(v.SummaryLevel),
			markSummary(v.Facts),
		)
	}

	top := views[0]
	fmt.Println()
	fmt.Printf("most-recent session detail — %s %s (level: %s)\n",
		top.Session.Runtime, shortName(top.Session.SessionID), strings.ToUpper(top.SummaryLevel))
	fmt.Printf("  cwd: %s\n", shortenHome(top.Session.CWD))
	for _, ss := range top.SourceStatus {
		if !ss.Available {
			fmt.Printf("  %s source %s unavailable: %s\n", gray("·"), ss.Source, ss.Reason)
		}
	}
	for _, f := range top.Facts {
		fmt.Printf("  %s %-18s %s — %s\n", statusGlyph(f.Status), f.Status, factName(f.Key), factDetail(f))
	}
}

// markSummary collapses facts into a compact "✓3 ✗1 ⚠1 ·2" summary by status class.
func markSummary(facts []fact.FactResult) string {
	var good, bad, conflict, gap int
	for _, f := range facts {
		switch f.Status {
		case fact.StatusExpectedObserved, fact.StatusExpectedVerified:
			good++
		case fact.StatusMissingExpected:
			bad++
		case fact.StatusConflict:
			conflict++
		default:
			gap++
		}
	}
	var parts []string
	if good > 0 {
		parts = append(parts, green(fmt.Sprintf("✓%d", good)))
	}
	if bad > 0 {
		parts = append(parts, red(fmt.Sprintf("✗%d", bad)))
	}
	if conflict > 0 {
		parts = append(parts, magenta(fmt.Sprintf("⚠%d", conflict)))
	}
	if gap > 0 {
		parts = append(parts, gray(fmt.Sprintf("·%d", gap)))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func statusGlyph(s fact.Status) string {
	switch s {
	case fact.StatusExpectedVerified:
		return green("✓✓")
	case fact.StatusExpectedObserved:
		return green("✓")
	case fact.StatusMissingExpected:
		return red("✗")
	case fact.StatusConflict:
		return magenta("⚠")
	default:
		return gray("·")
	}
}

func factName(k fact.FactKey) string {
	if k.Kind == fact.ToolAvailable {
		return "tool:" + k.Name
	}
	return k.Name
}

func factDetail(f fact.FactResult) string {
	switch f.Status {
	case fact.StatusExpectedVerified:
		return "verified on outbound request"
	case fact.StatusExpectedObserved:
		return "observed in transcript"
	case fact.StatusMissingExpected:
		return "expected but absent from complete catalog (drift)"
	case fact.StatusConflict:
		return "sources disagree — transcript vs wire"
	case fact.StatusCoverageGap:
		return "expected; no source can prove presence/absence here"
	case fact.StatusUnexpected:
		return "present but not expected"
	default:
		return string(f.Status)
	}
}

func shortenHome(p string) string {
	if p == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return "…" + s[len(s)-(max-1):]
}

func shortName(id string) string {
	if id == "" {
		return "(unknown)"
	}
	base := filepath.Base(id)
	if i := strings.IndexByte(base, '-'); i > 0 && len(base) > 12 {
		return base[:i]
	}
	if len(base) > 12 {
		return base[:12]
	}
	return base
}
