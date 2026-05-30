package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex transcript schema (verified 2026-05 against cli_version 0.134.0):
//
// Location (recent, preferred): ~/.codex/sessions/YYYY/MM/DD/rollout-<ISO>-<uuid>.jsonl
//   - JSONL, one record per line.
//
// Legacy (2025): ~/.codex/sessions/rollout-YYYY-MM-DD-<uuid>.json
//   - NOT JSONL: a single pretty-printed JSON object {session, items}.
//   - Intentionally NOT parsed here (task says prefer the nested recent files);
//     documented for the verifier.
//
// Each JSONL line: { "timestamp", "type", "payload" }. Observed top-level types:
//   session_meta, turn_context, event_msg, response_item.
//
// session_meta.payload keys:
//   id, timestamp, cwd, originator, cli_version, source, thread_source,
//   model_provider, base_instructions.
//   -> CWD = payload.cwd, Version = payload.cli_version, SessionID = payload.id.
//   -> base_instructions is an OBJECT {text: "..."} (the Codex base system prompt).
//
// Injected AGENTS.md / system-prompt:
//   (a) session_meta.payload.base_instructions.text  (Codex base prompt)
//   (b) response_item.payload where payload.type == "message" -> payload.content[]
//       blocks with .text — the user/developer messages carry AGENTS.md content.
//
// Tools (invoked only; no full catalog is recorded in the transcript):
//   response_item.payload.type == "function_call"     -> payload.name
//   response_item.payload.type == "custom_tool_call"  -> payload.name

type codexRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID               string          `json:"id"`
	CWD              string          `json:"cwd"`
	CLIVersion       string          `json:"cli_version"`
	BaseInstructions *codexBaseInstr `json:"base_instructions"`
}

type codexBaseInstr struct {
	Text string `json:"text"`
}

type codexResponseItem struct {
	Type    string              `json:"type"`
	Name    string              `json:"name"`
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func discoverCodex(home string) []Session {
	root := filepath.Join(home, ".codex", "sessions")
	var out []Session

	// Walk the nested YYYY/MM/DD tree for *.jsonl. WalkDir is robust to
	// permission errors on individual entries.
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil // legacy flat *.json (single-object) intentionally skipped
		}
		if s, ok := parseCodexFile(path, home); ok {
			out = append(out, s)
		}
		return nil
	})
	return out
}

func parseCodexFile(path, home string) (Session, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false
	}
	defer f.Close()

	startFallback, mtime := fileTimes(path)
	s := Session{
		Runtime:   "codex",
		Path:      path,
		StartedAt: startFallback,
	}

	var first, last time.Time
	count := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r codexRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		count++

		if ts := parseTimestamp(r.Timestamp); !ts.IsZero() {
			if first.IsZero() || ts.Before(first) {
				first = ts
			}
			if ts.After(last) {
				last = ts
			}
		}

		if r.Type == "session_meta" && len(r.Payload) > 0 {
			var m codexSessionMeta
			if err := json.Unmarshal(r.Payload, &m); err == nil {
				if m.ID != "" {
					s.SessionID = m.ID
				}
				if m.CWD != "" {
					s.CWD = m.CWD
				}
				if m.CLIVersion != "" {
					s.Version = m.CLIVersion
				}
			}
		}
	}

	if count == 0 {
		return Session{}, false
	}

	s.RecordCount = count
	if !first.IsZero() {
		s.StartedAt = first
	}
	if !last.IsZero() {
		s.LastActivity = last
	} else {
		s.LastActivity = mtime
	}
	if s.SessionID == "" {
		s.SessionID = codexSessionIDFromName(filepath.Base(path))
	}
	s.GitRepo = repoFromCWD(s.CWD, home)
	// Codex does not record a git branch in its transcript.
	return s, true
}

// extractCodexContext returns catalogComplete=false always: Codex transcripts
// record only INVOKED tool calls (function_call/custom_tool_call), never the
// full assembled tool catalog. So a tool's absence here is not evidence of drift.
func extractCodexContext(s Session) (systemPromptBlocks []string, toolNames []string, catalogComplete bool, err error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, nil, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r codexRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		switch r.Type {
		case "session_meta":
			var m codexSessionMeta
			if err := json.Unmarshal(r.Payload, &m); err == nil {
				if m.BaseInstructions != nil && m.BaseInstructions.Text != "" {
					systemPromptBlocks = append(systemPromptBlocks, m.BaseInstructions.Text)
				}
			}
		case "response_item":
			var p codexResponseItem
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			switch p.Type {
			case "message":
				// developer/user messages carry injected instructions
				// (AGENTS.md etc.). Concatenate text blocks for this message.
				var parts []string
				for _, b := range p.Content {
					if b.Text != "" {
						parts = append(parts, b.Text)
					}
				}
				if len(parts) > 0 {
					systemPromptBlocks = append(systemPromptBlocks, strings.Join(parts, "\n"))
				}
			case "function_call", "custom_tool_call":
				if p.Name != "" {
					toolNames = append(toolNames, p.Name)
				}
			}
		}
	}

	return uniqueStrings(systemPromptBlocks), uniqueStrings(toolNames), false, nil
}

// codexSessionIDFromName extracts the uuid tail from rollout-<ISO>-<uuid>.jsonl.
func codexSessionIDFromName(name string) string {
	name = trimExt(name)
	// uuid is the last 5 dash-separated groups; simplest robust heuristic is the
	// final 36 chars if they look like a uuid, else the whole stem.
	if len(name) >= 36 {
		tail := name[len(name)-36:]
		if strings.Count(tail, "-") == 4 {
			return tail
		}
	}
	return name
}
