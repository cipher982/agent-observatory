package install_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cipher982/agent-observatory/backend/internal/install"
	"github.com/cipher982/agent-observatory/backend/internal/wire"
)

func TestAmbientInstallFreshProcessCapture(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	const reqBody = `{"system":"ambient install proof","tools":[{"name":"mcp__proof__run"}],"messages":[]}`
	var gotUpstreamBody []byte
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUpstreamBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)
	upHost, upPort, _ := net.SplitHostPort(upURL.Host)

	target := install.DefaultTarget(home, os.Args[0])
	srv, err := wire.NewServerStableCA(target.CADir, log.New(io.Discard, "", 0), time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.SetInspectHost(func(string) bool { return true })
	upRoots := x509.NewCertPool()
	upRoots.AddCert(upstream.Certificate())
	srv.SetUpstreamTLS(&tls.Config{RootCAs: upRoots, MinVersion: tls.VersionTLS12})
	proxyAddr, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	target.ProxyAddr = proxyAddr

	envSet := map[string]string{}
	target.LaunchctlSetenv = func(k, v string) error {
		envSet[k] = v
		return nil
	}
	if err := target.Install(); err != nil {
		t.Fatal(err)
	}
	if target.Status().Installed != true {
		t.Fatalf("target should report installed after stable CA + profile + plist")
	}
	// The install sets only the additive Node trust var; routing is the NE
	// extension's job, so HTTPS_PROXY is no longer part of the install.
	if envSet["NODE_EXTRA_CA_CERTS"] == "" {
		t.Fatalf("install should set NODE_EXTRA_CA_CERTS for Node trust")
	}

	// Model what the NE relay does for a captured flow: route the agent's request
	// to the local proxy (here via HTTPS_PROXY in the child env, since the test
	// can't install a kernel transparent proxy) and trust the stable CA the way
	// the install delivers it (NODE_EXTRA_CA_CERTS for the Node-style helper).
	cmd := exec.Command(os.Args[0], "-test.run=TestAmbientInstallFreshProcessCaptureHelper", "--")
	cmd.Env = append(os.Environ(),
		"OBS_AMBIENT_HELPER=1",
		"OBS_AMBIENT_URL=https://"+upHost+":"+upPort+"/v1/messages",
		"HTTPS_PROXY=http://"+proxyAddr,
	)
	for k, v := range envSet {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh helper process failed: %v\n%s", err, out)
	}
	if string(gotUpstreamBody) != reqBody {
		t.Fatalf("upstream body mismatch:\n got: %s\nwant: %s", gotUpstreamBody, reqBody)
	}
	caps := srv.Captures()
	if len(caps) != 1 {
		t.Fatalf("ambient fresh process produced %d captures, want 1: %+v", len(caps), caps)
	}
	if caps[0].Endpoint != "anthropic/messages" || len(caps[0].ToolNames) != 1 {
		t.Fatalf("capture = %+v, want anthropic/messages with one tool", caps[0])
	}
}

func TestAmbientInstallFreshProcessCaptureHelper(t *testing.T) {
	if os.Getenv("OBS_AMBIENT_HELPER") != "1" {
		t.Skip("helper only")
	}
	proxyURL, err := url.Parse(os.Getenv("HTTPS_PROXY"))
	if err != nil {
		t.Fatal(err)
	}
	// Trust the stable CA the way the install delivers it to Node: the additive
	// NODE_EXTRA_CA_CERTS var (never SSL_CERT_FILE, which would replace roots).
	caPEM, err := os.ReadFile(os.Getenv("NODE_EXTRA_CA_CERTS"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("could not trust Observatory CA")
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		ProxyConnectHeader: http.Header{
			"X-Agent-Observatory-Transparent-Flow":  {"1"},
			"X-Agent-Observatory-Source-Signing-ID": {"com.anthropic.claude-code"},
			"X-Agent-Observatory-Source-PID":        {fmt.Sprint(os.Getpid())},
		},
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}, Timeout: 10 * time.Second}
	body := []byte(`{"system":"ambient install proof","tools":[{"name":"mcp__proof__run"}],"messages":[]}`)
	resp, err := client.Post(os.Getenv("OBS_AMBIENT_URL"), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}
