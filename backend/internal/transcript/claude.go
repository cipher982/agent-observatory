package transcript

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Claude Code transcript schema (verified 2026-05 against v2.1.152):
//
// Location: ~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//   - one JSONL file per session; the directory name is the cwd with '/' -> '-'.
//
// Each line is a JSON object with a "type" field. Observed types:
//   user, assistant, system, attachment, file-history-snapshot, mode,
//   permission-mode, last-prompt, ai-title.
//
// Most records (user/assistant/system/attachment) carry top-level:
//   cwd, gitBranch, sessionId, version, timestamp, uuid, parentUuid.
//
// Injected AGENTS.md / CLAUDE.md content:
//   record type == "attachment" AND .attachment.type == "file"
//   -> .attachment.content.file.{filePath, content, numLines, ...}
//   The global prompt's marker "Behavior gates" lives in that content string.
//
// Tools:
//   (a) Full registered catalog: attachment with .attachment.type ==
//       "deferred_tools_delta" -> .attachment.addedNames []string.
//   (b) Actually-invoked tools: assistant record ->
//       .message.content[] where block.type == "tool_use" -> block.name.

type claudeRecord struct {
	Type       string          `json:"type"`
	CWD        string          `json:"cwd"`
	GitBranch  string          `json:"gitBranch"`
	SessionID  string          `json:"sessionId"`
	Version    string          `json:"version"`
	Timestamp  string          `json:"timestamp"`
	Attachment *claudeAttach   `json:"attachment,omitempty"`
	Message    *claudeMessage  `json:"message,omitempty"`
}

type claudeAttach struct {
	Type       string            `json:"type"`
	AddedNames []string          `json:"addedNames,omitempty"`
	Content    *claudeAttachBody `json:"content,omitempty"`
}

type claudeAttachBody struct {
	File *claudeFile `json:"file,omitempty"`
}

type claudeFile struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
}

type claudeMessage struct {
	Content []claudeContentBlock `json:"content"`
}

type claudeContentBlock struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

func discoverClaude(home string) []Session {
	root := filepath.Join(home, ".claude", "projects")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var out []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projDir := filepath.Join(root, e.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".jsonl" {
				continue
			}
			path := filepath.Join(projDir, f.Name())
			if s, ok := parseClaudeFile(path, home); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func parseClaudeFile(path, home string) (Session, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false
	}
	defer f.Close()

	startFallback, mtime := fileTimes(path)
	s := Session{
		Runtime:   "claude",
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
		var r claudeRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue // skip malformed line, never panic
		}
		count++

		if s.SessionID == "" && r.SessionID != "" {
			s.SessionID = r.SessionID
		}
		if r.CWD != "" {
			s.CWD = r.CWD
		}
		if r.GitBranch != "" {
			s.GitBranch = r.GitBranch
		}
		if r.Version != "" {
			s.Version = r.Version
		}
		if ts := parseTimestamp(r.Timestamp); !ts.IsZero() {
			if first.IsZero() || ts.Before(first) {
				first = ts
			}
			if ts.After(last) {
				last = ts
			}
		}
	}
	// scanner errors (e.g. a single overlong line) are tolerated: we keep
	// whatever we parsed up to that point.

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
		// fall back to the file name (it is the session uuid)
		s.SessionID = trimExt(filepath.Base(path))
	}
	s.GitRepo = repoFromCWD(s.CWD, home)
	return s, true
}

func extractClaudeContext(s Session) (systemPromptBlocks []string, toolNames []string, catalogComplete bool, err error) {
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
		var r claudeRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		switch r.Type {
		case "attachment":
			if r.Attachment == nil {
				continue
			}
			switch r.Attachment.Type {
			case "file":
				if r.Attachment.Content != nil && r.Attachment.Content.File != nil {
					if c := r.Attachment.Content.File.Content; c != "" {
						systemPromptBlocks = append(systemPromptBlocks, c)
					}
				}
			case "deferred_tools_delta":
				// This record enumerates the full tool catalog made available.
				toolNames = append(toolNames, r.Attachment.AddedNames...)
				catalogComplete = true
			}
		case "assistant":
			if r.Message == nil {
				continue
			}
			for _, b := range r.Message.Content {
				if b.Type == "tool_use" && b.Name != "" {
					toolNames = append(toolNames, b.Name)
				}
			}
		}
	}

	return uniqueStrings(systemPromptBlocks), uniqueStrings(toolNames), catalogComplete, nil
}

func trimExt(name string) string {
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)]
}
