package wire

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/fact"
	"github.com/cipher982/agent-observatory/backend/internal/resolver"
)

// hostRuntime mirrors monitor.go's runtimeForHost closely enough for the test:
// anthropic/bedrock => claude, openai => codex.
func hostRuntime(host string) string {
	switch {
	case strings.Contains(host, "anthropic"), strings.Contains(host, "bedrock"):
		return "claude"
	case strings.Contains(host, "openai"):
		return "codex"
	default:
		return "unknown"
	}
}

// TestObservationsForRuntimeFiltersByHost proves a captured OpenAI/Codex request
// never attributes to a Claude session, and vice versa — the ambient daemon must
// not cross-attribute verified facts across runtimes.
func TestObservationsForRuntimeFiltersByHost(t *testing.T) {
	srv, err := NewServer(t.TempDir(), log.New(io.Discard, "", 0), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv.Inject(Capture{Host: "api.anthropic.com", Endpoint: "anthropic/messages",
		AllText: "claude system text", ToolNames: []string{"mcp__alpha__run"}})
	srv.Inject(Capture{Host: "api.openai.com", Endpoint: "openai/responses",
		AllText: "codex system text", ToolNames: []string{"mcp__beta__run"}})

	claudeObs := srv.ObservationsForRuntime("claude", "sess-c", resolver.Resolution{}, hostRuntime)
	codexObs := srv.ObservationsForRuntime("codex", "sess-x", resolver.Resolution{}, hostRuntime)

	if hasTool(claudeObs, "beta") {
		t.Error("claude session got a codex (openai) tool observation")
	}
	if !hasTool(claudeObs, "alpha") {
		t.Error("claude session missing its own anthropic tool observation")
	}
	if hasTool(codexObs, "alpha") {
		t.Error("codex session got a claude (anthropic) tool observation")
	}
	if !hasTool(codexObs, "beta") {
		t.Error("codex session missing its own openai tool observation")
	}
}

func hasTool(obs []fact.Observation, server string) bool {
	for _, o := range obs {
		if o.Key.Kind == fact.ToolAvailable && o.Key.Name == server {
			return true
		}
	}
	return false
}
