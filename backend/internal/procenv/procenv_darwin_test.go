//go:build darwin

package procenv

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestParseProcargs2(t *testing.T) {
	buf := procargs2Buffer(
		[]string{"/opt/homebrew/bin/node", "/opt/homebrew/bin/gemini", "--model", "gemini-2.5-pro"},
		[]string{"NODE_EXTRA_CA_CERTS=/tmp/observatory-ca.pem", "OTHER=value"},
	)
	info, err := parseProcargs2(buf)
	if err != nil {
		t.Fatal(err)
	}
	if info.Command != "/opt/homebrew/bin/node /opt/homebrew/bin/gemini --model gemini-2.5-pro" {
		t.Fatalf("command = %q", info.Command)
	}
	if got := info.Env["NODE_EXTRA_CA_CERTS"]; got != "/tmp/observatory-ca.pem" {
		t.Fatalf("NODE_EXTRA_CA_CERTS = %q", got)
	}
}

func procargs2Buffer(args, env []string) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(args)))
	buf.WriteString(args[0])
	buf.WriteByte(0)
	buf.WriteByte(0)
	for _, arg := range args {
		buf.WriteString(arg)
		buf.WriteByte(0)
	}
	buf.WriteByte(0)
	for _, value := range env {
		buf.WriteString(value)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}
