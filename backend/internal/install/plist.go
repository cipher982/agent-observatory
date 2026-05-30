package install

import (
	"fmt"
	"os"
	"path/filepath"
)

// writePlist writes the launchd LaunchAgent that keeps the monitor daemon
// running (proxy + API + live stream). RunAtLoad + KeepAlive => always-on.
func (t Target) writePlist() error {
	if err := os.MkdirAll(t.LaunchDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(t.StateDir, "daemon.log")
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>monitor</string>
        <string>--port</string>
        <string>%d</string>
        <string>--proxy-port</string>
        <string>%s</string>
        <string>--ca-dir</string>
        <string>%s</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, t.label(), t.BinPath, t.APIPort, portOf(t.ProxyAddr), t.CADir, logPath, logPath)
	return os.WriteFile(t.plistPath(), []byte(plist), 0o644)
}

// portOf extracts the port from host:port.
func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i+1:]
		}
	}
	return addr
}
