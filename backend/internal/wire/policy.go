package wire

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/cipher982/agent-observatory/backend/internal/procenv"
)

const (
	headerTransparentFlow = "X-Agent-Observatory-Transparent-Flow"
	headerSourceSigningID = "X-Agent-Observatory-Source-Signing-ID"
	headerSourcePID       = "X-Agent-Observatory-Source-PID"
)

// FlowMetadata is the source identity the NetworkExtension forwards to the
// daemon. Missing metadata is allowed: explicit proxy/dev paths keep their old
// inspect behavior, while transparent provider flows require a supported source.
type FlowMetadata struct {
	Transparent     bool
	SourceSigningID string
	SourcePID       int
}

type captureDecision struct {
	inspect bool
	runtime string
	reason  string
}

type processInfo = procenv.Info

type processLookup interface {
	LookupProcess(pid int) (processInfo, error)
}

type osProcessLookup struct{}

func (osProcessLookup) LookupProcess(pid int) (processInfo, error) {
	return procenv.Lookup(pid)
}

// CapturePolicy decides whether a provider flow is safe to TLS-terminate. The
// safe default for transparent capture is "tunnel unless the source is a
// supported runtime with current Observatory trust".
type CapturePolicy struct {
	caPath                     string
	lookup                     processLookup
	requireTransparentMetadata bool
}

func NewCapturePolicy(caPath string) *CapturePolicy {
	return &CapturePolicy{caPath: caPath, lookup: osProcessLookup{}}
}

func flowMetadataFromRequest(r *http.Request) FlowMetadata {
	pid, _ := strconv.Atoi(strings.TrimSpace(r.Header.Get(headerSourcePID)))
	return FlowMetadata{
		Transparent:     r.Header.Get(headerTransparentFlow) == "1",
		SourceSigningID: strings.TrimSpace(r.Header.Get(headerSourceSigningID)),
		SourcePID:       pid,
	}
}

func (p *CapturePolicy) Decide(meta FlowMetadata) captureDecision {
	if p == nil {
		return captureDecision{inspect: true, reason: "explicit proxy/dev flow"}
	}
	if !meta.Transparent {
		if p.requireTransparentMetadata {
			return captureDecision{inspect: false, reason: "missing transparent source metadata"}
		}
		return captureDecision{inspect: true, reason: "explicit proxy/dev flow"}
	}

	info := processInfo{Env: map[string]string{}}
	if meta.SourcePID > 0 && p.lookup != nil {
		if got, err := p.lookup.LookupProcess(meta.SourcePID); err == nil {
			info = got
			if info.Env == nil {
				info.Env = map[string]string{}
			}
		}
	}

	sig := strings.TrimSpace(meta.SourceSigningID)
	cmd := strings.ToLower(info.Command)
	switch {
	case sig == "com.anthropic.claude-code":
		return p.envDecision("claude", info, "NODE_EXTRA_CA_CERTS")
	case sig == "codex":
		return p.envDecision("codex", info, "CODEX_CA_CERTIFICATE")
	case isGeminiNodeSource(sig, cmd):
		return p.envDecision("gemini", info, "NODE_EXTRA_CA_CERTS")
	default:
		return captureDecision{inspect: false, reason: "unsupported or unknown transparent source"}
	}
}

func (p *CapturePolicy) envDecision(runtime string, info processInfo, key string) captureDecision {
	if p.caPath != "" && info.Env[key] == p.caPath {
		return captureDecision{inspect: true, runtime: runtime, reason: "source has current Observatory trust"}
	}
	return captureDecision{inspect: false, runtime: runtime, reason: key + " missing or stale"}
}

func isGeminiNodeSource(signingID, command string) bool {
	if !strings.HasPrefix(signingID, "node-") && !strings.Contains(command, "/node") && !strings.HasPrefix(command, "node ") {
		return false
	}
	return strings.Contains(command, "gemini")
}
