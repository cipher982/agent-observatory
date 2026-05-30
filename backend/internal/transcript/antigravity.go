package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Antigravity CLI (the successor to the deprecated Gemini CLI, retired June 2026).
//
// Layout: ~/.gemini/antigravity-cli/
//   conversations/<uuid>.pb   — per-conversation transcript, OPAQUE: the bytes are
//                                encrypted/compressed (measured entropy ~8.0
//                                bits/byte, no readable strings). We CANNOT read
//                                the assembled system prompt or tools from these.
//   history.jsonl             — append-only index: {display, timestamp(ms),
//                                workspace, conversationId?}. Maps a conversation
//                                to its workspace and last-activity time.
//
// Therefore Antigravity has Coverage NONE for prompt/tools: we surface that an
// agent session EXISTS and WHERE (workspace), but we never fake context facts.
// This is the honest "discovery-only" runtime.

// antigravityHistory maps each conversationId to its workspace + latest activity.
type antigravityHistEntry struct {
	workspace string
	last      time.Time
	count     int
}

func loadAntigravityHistory(home string) map[string]antigravityHistEntry {
	path := filepath.Join(home, ".gemini", "antigravity-cli", "history.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	out := map[string]antigravityHistEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
			Timestamp      int64  `json:"timestamp"` // epoch millis
		}
		if json.Unmarshal(line, &rec) != nil || rec.ConversationID == "" {
			continue
		}
		ts := time.UnixMilli(rec.Timestamp)
		e := out[rec.ConversationID]
		e.count++
		if rec.Workspace != "" {
			e.workspace = rec.Workspace
		}
		if ts.After(e.last) {
			e.last = ts
		}
		out[rec.ConversationID] = e
	}
	return out
}

func discoverAntigravity(home string) []Session {
	convDir := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		return nil
	}
	hist := loadAntigravityHistory(home)
	var out []Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pb" {
			continue
		}
		if s, ok := parseAntigravityFile(filepath.Join(convDir, e.Name()), home, hist); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseAntigravityFile builds a Session from the conversation file's mtime plus
// the history index. The .pb body is never read (opaque). cwd/last-activity come
// from history.jsonl keyed by the conversationId (= filename stem).
func parseAntigravityFile(path, home string, hist map[string]antigravityHistEntry) (Session, bool) {
	convID := trimExt(filepath.Base(path))
	start, mtime := fileTimes(path)
	s := Session{
		Runtime:      "antigravity",
		SessionID:    convID,
		Path:         path,
		StartedAt:    start,
		LastActivity: mtime,
	}
	if e, ok := hist[convID]; ok {
		s.CWD = e.workspace
		s.RecordCount = e.count
		if !e.last.IsZero() {
			s.LastActivity = e.last
		}
	}
	s.GitRepo = repoFromCWD(s.CWD, home)
	return s, true
}

// extractAntigravityContext: the .pb conversation files are opaque
// (encrypted/compressed), so no system prompt or tools can be extracted. Returns
// empty with catalogComplete=false — the verifier treats this as Coverage NONE
// and never asserts presence or absence of context facts for Antigravity.
func extractAntigravityContext(s Session) (systemPromptBlocks []string, toolNames []string, catalogComplete bool, err error) {
	return nil, nil, false, nil
}
