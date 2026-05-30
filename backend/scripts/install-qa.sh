#!/usr/bin/env bash
# Install-lifecycle QA against the REAL `agents` binary, in a throwaway fake HOME,
# looped N times to prove repeatability. Never touches the real shell/launchd:
# we point the binary at a fake HOME and stub launchctl/launchd via a fake PATH.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ITER="${1:-5}"
BIN="$(mktemp -d)/agents"
go build -o "$BIN" ./cmd/agents

# Fake launchctl so install/uninstall don't touch the real launchd.
STUBDIR="$(mktemp -d)"
cat > "$STUBDIR/launchctl" <<'EOF'
#!/usr/bin/env bash
# record-only stub
echo "launchctl $*" >> "${OBS_LAUNCHCTL_LOG:-/dev/null}"
exit 0
EOF
chmod +x "$STUBDIR/launchctl"

pass=0
for i in $(seq 1 "$ITER"); do
  FAKE_HOME="$(mktemp -d)"
  # seed a realistic pre-existing profile
  printf 'export PATH=/usr/bin\nalias g=git\n' > "$FAKE_HOME/.zshenv"
  BEFORE="$(cat "$FAKE_HOME/.zshenv")"
  export OBS_LAUNCHCTL_LOG="$FAKE_HOME/launchctl.log"

  # install (fake HOME + stubbed launchctl on PATH)
  HOME="$FAKE_HOME" PATH="$STUBDIR:$PATH" "$BIN" install >/dev/null

  # simulate the daemon creating the CA on first run (launchd is stubbed)
  echo "FAKE CA" > "$FAKE_HOME/.local/state/agent-observatory/ca/observatory-ca.pem"

  # verify installed
  HOME="$FAKE_HOME" PATH="$STUBDIR:$PATH" "$BIN" status >/dev/null || { echo "iter $i: status not installed"; exit 1; }
  grep -q "agent-observatory" "$FAKE_HOME/.zshenv" || { echo "iter $i: no managed block"; exit 1; }
  test -f "$FAKE_HOME/Library/LaunchAgents/io.drose.observatory.plist" || { echo "iter $i: no plist"; exit 1; }
  grep -q "launchctl setenv HTTPS_PROXY" "$OBS_LAUNCHCTL_LOG" || { echo "iter $i: setenv not called"; exit 1; }

  # double-install (idempotency)
  HOME="$FAKE_HOME" PATH="$STUBDIR:$PATH" "$BIN" install >/dev/null
  blocks=$(grep -c ">>> agent-observatory >>>" "$FAKE_HOME/.zshenv")
  test "$blocks" -eq 1 || { echo "iter $i: $blocks managed blocks after double-install"; exit 1; }

  # uninstall
  HOME="$FAKE_HOME" PATH="$STUBDIR:$PATH" "$BIN" uninstall >/dev/null

  # assert clean: profile restored exactly, plist + CA gone
  AFTER="$(cat "$FAKE_HOME/.zshenv")"
  test "$BEFORE" = "$AFTER" || { echo "iter $i: profile not restored:"; diff <(echo "$BEFORE") <(echo "$AFTER"); exit 1; }
  test ! -f "$FAKE_HOME/Library/LaunchAgents/io.drose.observatory.plist" || { echo "iter $i: plist survived"; exit 1; }
  test ! -d "$FAKE_HOME/.local/state/agent-observatory/ca" || { echo "iter $i: CA dir survived"; exit 1; }

  rm -rf "$FAKE_HOME"
  pass=$((pass+1))
done

echo "install-qa: $pass/$ITER iterations clean (install→verify→double-install→uninstall→assert-clean)"
