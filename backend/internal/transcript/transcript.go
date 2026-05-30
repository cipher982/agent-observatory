// Package transcript discovers live/recent coding-agent sessions by reading the
// on-disk JSONL transcripts that the CLIs already write: filesystem only, no
// wire interception.
//
// Supported runtimes:
//   - "claude"      : ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//   - "codex"       : ~/.codex/sessions/YYYY/MM/DD/rollout-<ISO>-<uuid>.jsonl
//   - "antigravity" : ~/.gemini/antigravity-cli/conversations/<uuid>.pb (opaque;
//     discovery-only via history.jsonl — Coverage NONE for context)
//
// (The deprecated Gemini CLI, retired June 2026, is intentionally NOT read; its
// old ~/.gemini/tmp/.../chats path only holds pre-deprecation ghosts.)
//
// All parsers are defensive: malformed lines are skipped, a bad file never panics
// and never aborts the whole scan.
package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is a single discovered agent session.
type Session struct {
	Runtime      string    // "claude" | "codex" | "antigravity"
	SessionID    string    // runtime session id (best effort)
	Path         string    // transcript file on disk
	CWD          string    // working directory of the session, if known
	GitRepo      string    // best-effort repo name derived from CWD
	GitBranch    string    // git branch if the runtime records it (claude only)
	StartedAt    time.Time // first record timestamp, or file ctime fallback
	LastActivity time.Time // max record timestamp, or file mtime fallback
	Version      string    // CLI version if available
	RecordCount  int       // number of parsed records in the transcript
}

// Discover scans every known runtime directory, parses each transcript, and
// returns the sessions sorted by LastActivity descending (most recent first).
// Errors reading individual files are swallowed; Discover only returns an error
// for a catastrophic failure (currently it never does, but the signature leaves
// room for one).
func Discover() ([]Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var out []Session
	out = append(out, discoverClaude(home)...)
	out = append(out, discoverCodex(home)...)
	out = append(out, discoverAntigravity(home)...)

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	return out, nil
}

// candidate is a transcript file located cheaply (no content read) with its
// modification time, so we can rank by recency before doing expensive parsing.
type candidate struct {
	path    string
	runtime string
	mtime   time.Time
	cwd     string // pre-resolved cwd for runtimes that need it (antigravity)
}

// DiscoverRecent returns at most `limit` most-recent sessions WITHOUT fully
// parsing every transcript on disk. It first collects candidate files with only
// their mtime (a stat, no read), sorts by mtime descending, then fully parses
// just the top `limit`. This keeps latency flat even with thousands of
// transcripts — the slow full Discover() reads every file, which does not scale
// for an interactive, polled API.
func DiscoverRecent(limit int) ([]Session, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	var cands []candidate
	cands = append(cands, claudeCandidates(home)...)
	cands = append(cands, codexCandidates(home)...)
	cands = append(cands, antigravityCandidates(home)...)

	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].mtime.After(cands[j].mtime)
	})

	// Parse a modest multiple of the limit, since some candidates may fail to
	// parse (legacy formats, partial files); then trim to limit after sorting by
	// the precise in-file LastActivity.
	budget := limit * 2
	if budget < 20 {
		budget = 20
	}

	var out []Session
	for _, c := range cands {
		if len(out) >= budget {
			break
		}
		switch c.runtime {
		case "claude":
			if s, ok := parseClaudeFile(c.path, home); ok {
				out = append(out, s)
			}
		case "codex":
			if s, ok := parseCodexFile(c.path, home); ok {
				out = append(out, s)
			}
		case "antigravity":
			if s, ok := parseAntigravityFile(c.path, home, loadAntigravityHistory(home)); ok {
				out = append(out, s)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastActivity.After(out[j].LastActivity)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Assembled is the context a CLI actually recorded assembling for a session.
type Assembled struct {
	SystemPromptBlocks []string
	ToolNames          []string
	// ToolCatalogComplete is true only when the transcript records the FULL set
	// of tools made available to the agent (Claude's deferred_tools_delta). When
	// false, ToolNames holds only the tools the agent happened to INVOKE (Codex),
	// so the absence of a tool proves nothing — the OBSERVED verifier must not
	// red-mark a missing tool in that case.
	ToolCatalogComplete bool
}

// ExtractAssembled pulls the assembled context for a session, including whether
// the captured tool list is a complete catalog or merely invoked tools.
func ExtractAssembled(s Session) (Assembled, error) {
	switch s.Runtime {
	case "claude":
		blocks, tools, complete, err := extractClaudeContext(s)
		return Assembled{blocks, tools, complete}, err
	case "codex":
		blocks, tools, complete, err := extractCodexContext(s)
		return Assembled{blocks, tools, complete}, err
	case "antigravity":
		blocks, tools, complete, err := extractAntigravityContext(s)
		return Assembled{blocks, tools, complete}, err
	default:
		return Assembled{}, nil
	}
}

// ExtractAssembledContext is the back-compatible accessor returning just blocks
// and tool names. Prefer ExtractAssembled when catalog-completeness matters.
func ExtractAssembledContext(s Session) (systemPromptBlocks []string, toolNames []string, err error) {
	a, err := ExtractAssembled(s)
	return a.SystemPromptBlocks, a.ToolNames, err
}

// repoFromCWD derives a best-effort repo name from a cwd. Preference order:
//  1. the path segment immediately under "<home>/git" (e.g. ~/git/workspace -> "workspace"),
//  2. otherwise the last path segment.
//
// It is deliberately heuristic; the verifier should treat it as a hint.
func repoFromCWD(cwd, home string) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	gitRoot := filepath.Join(home, "git") + string(filepath.Separator)
	if strings.HasPrefix(cwd+string(filepath.Separator), gitRoot) {
		rest := strings.TrimPrefix(cwd, gitRoot)
		if i := strings.IndexRune(rest, filepath.Separator); i >= 0 {
			return rest[:i]
		}
		if rest != "" {
			return rest
		}
	}
	return filepath.Base(cwd)
}

// fileTimes returns (ctime-ish StartedAt fallback, mtime) for a path.
func fileTimes(path string) (time.Time, time.Time) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, time.Time{}
	}
	mt := fi.ModTime()
	return mt, mt
}

// uniqueStrings preserves first-seen order and drops empties/dupes.
func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// parseTimestamp parses the RFC3339 timestamps the CLIs emit. Returns zero time
// on failure.
func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
