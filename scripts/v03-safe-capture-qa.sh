#!/usr/bin/env bash
# v0.3 safe-capture policy QA.
#
# This is intentionally narrower than full app/NetworkExtension live QA. It
# starts the local daemon, simulates transparent provider flows with source
# metadata, and proves:
#   1. supported + current trust => full request-body capture
#   2. unknown source => opaque pass-through, no broken request
#   3. /api/coverage reports both the capture and bypass
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
TMP="$(mktemp -d)"
API_PORT="${API_PORT:-18778}"
PROXY_PORT="${PROXY_PORT:-18779}"
MONITOR_PID=""

cleanup() {
  if [ -n "$MONITOR_PID" ]; then
    kill "$MONITOR_PID" >/dev/null 2>&1 || true
    wait "$MONITOR_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP"
}
trap cleanup EXIT

echo "==> build QA binaries"
(cd "$BACKEND" && go build -o "$TMP/agents" ./cmd/agents)
(cd "$BACKEND" && go build -o "$TMP/wirepolicyqa" ./cmd/wirepolicyqa)

echo "==> focused policy tests"
(cd "$BACKEND" && go test ./internal/procenv ./internal/wire -run 'TestParseProcargs2|TestTransparent|TestParseBodyVariants' -count=1)

echo "==> start monitor on 127.0.0.1:$API_PORT / proxy $PROXY_PORT"
"$TMP/agents" monitor --port "$API_PORT" --proxy-port "$PROXY_PORT" --ca-dir "$TMP/ca" >"$TMP/monitor.log" 2>&1 &
MONITOR_PID="$!"

for _ in $(seq 1 80); do
  if curl -fsS "http://127.0.0.1:$API_PORT/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$API_PORT/healthz" >/dev/null

echo "==> provider capture + bypass smoke"
CODEX_CA_CERTIFICATE="$TMP/ca/observatory-ca.pem" \
  "$TMP/wirepolicyqa" \
  --api "http://127.0.0.1:$API_PORT" \
  --proxy "http://127.0.0.1:$PROXY_PORT" \
  --ca "$TMP/ca/observatory-ca.pem"

echo "==> coverage"
curl -fsS "http://127.0.0.1:$API_PORT/api/coverage"
echo
echo "v03-safe-capture-qa: OK"
