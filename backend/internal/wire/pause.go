package wire

import (
	"os"
	"strings"
)

const defaultCapturePausePath = "/tmp/agent-observatory-capture-paused"

var capturePausePath = defaultCapturePausePath

// CapturePausePath is the cross-process circuit-breaker marker shared with the
// NetworkExtension. When present, the extension passes provider flows through
// directly instead of routing them into the MITM proxy.
func CapturePausePath() string { return capturePausePath }

// PauseCapture writes the marker that tells the NetworkExtension to stop
// capturing new flows. Existing in-flight flows are unaffected.
func PauseCapture(reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "capture paused"
	}
	return os.WriteFile(capturePausePath, []byte(reason+"\n"), 0o644)
}

// ClearCapturePause removes the circuit-breaker marker.
func ClearCapturePause() error {
	err := os.Remove(capturePausePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// CapturePaused reports whether the circuit breaker is currently active.
func CapturePaused() (bool, string) {
	data, err := os.ReadFile(capturePausePath)
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(data))
}

// SetCapturePausePathForTest redirects the global marker path for tests.
func SetCapturePausePathForTest(path string) func() {
	old := capturePausePath
	capturePausePath = path
	return func() { capturePausePath = old }
}
