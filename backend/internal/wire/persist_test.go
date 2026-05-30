package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPersistRedactsBodies proves the v2 privacy rule: persisted captures store
// only DERIVED facts (length, endpoint, tool names) — never the raw system prompt.
func TestPersistRedactsBodies(t *testing.T) {
	sensitive := "SENSITIVE PROMPT with private release instructions"
	caps := []Capture{{
		Host:         "bedrock-runtime.us-east-1.amazonaws.com",
		Endpoint:     "bedrock/invoke",
		SystemPrompt: sensitive,
		ToolNames:    []string{"mcp__search__query"},
		When:         time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC),
	}}

	dir := t.TempDir()
	path := filepath.Join(dir, "wire-claude.json")
	if err := WriteCaptures(path, caps); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), sensitive) {
		t.Fatalf("persisted file leaked the raw system prompt — privacy violation")
	}
	if !strings.Contains(string(raw), "bedrock/invoke") || !strings.Contains(string(raw), "systemChars") {
		t.Errorf("persisted file should retain derived facts")
	}

	// Round-trip: derived facts survive, SystemPrompt does not.
	back, err := ReadCaptures(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].SystemPrompt != "" {
		t.Errorf("ReadCaptures should not restore raw prompt, got %q", back[0].SystemPrompt)
	}
	if len(back[0].ToolNames) != 1 {
		t.Errorf("derived facts lost in round-trip: %+v", back[0])
	}
}
