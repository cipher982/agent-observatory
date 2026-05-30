package wire

import (
	"encoding/json"
	"os"
)

// Persisted captures let `agents run` hand VERIFIED evidence to a later
// `agents sessions` call. We deliberately store only the DERIVED facts
// (endpoint, system prompt length, tool names) — NOT raw
// request bodies — so sensitive prompt content is never written to disk by
// default (per the v2 "don't store decrypted bodies" rule).

type persistedCapture struct {
	Host        string   `json:"host"`
	Endpoint    string   `json:"endpoint"`
	SystemChars int      `json:"systemChars"`
	ToolNames   []string `json:"toolNames"`
	When        string   `json:"when"`
}

// WriteCaptures persists the derived (redacted) form of captures to path.
func WriteCaptures(path string, caps []Capture) error {
	out := make([]persistedCapture, 0, len(caps))
	for _, c := range caps {
		out = append(out, persistedCapture{
			Host:        c.Host,
			Endpoint:    c.Endpoint,
			SystemChars: len([]rune(c.SystemPrompt)),
			ToolNames:   c.ToolNames,
			When:        c.When.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ReadCaptures loads persisted captures back into Capture form (SystemPrompt is
// not restored — only derived facts like length and tool names are retained.
func ReadCaptures(path string) ([]Capture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in []persistedCapture
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	out := make([]Capture, 0, len(in))
	for _, p := range in {
		out = append(out, Capture{
			Host:      p.Host,
			Endpoint:  p.Endpoint,
			ToolNames: p.ToolNames,
		})
	}
	return out, nil
}
