#!/usr/bin/env bash
# v0.3 installed-daemon compatibility QA.
#
# This is narrower than v03-installed-ne-proof: it does NOT require the 0.3
# System Extension to be active. It proves the installed daemon fails open when
# a provider flow reaches the local proxy without v0.3 source metadata, which is
# the old-extension/malformed-relay safety case.
set -euo pipefail

VERSION="${VERSION:-0.3.0}"
APP_PATH="${APP_PATH:-/Applications/Agent Observatory.app}"
API="${API:-http://127.0.0.1:7878}"

fail() {
  echo "v03-installed-daemon-compat-qa: FAIL: $*" >&2
  exit 1
}

ok() {
  echo "v03-installed-daemon-compat-qa: OK: $*"
}

json_get() {
  local key="$1"
  local body="$2"
  printf '%s' "$body" | plutil -extract "$key" raw -o - - 2>/dev/null
}

numeric() {
  local value="$1"
  [[ "$value" =~ ^[0-9]+$ ]]
}

HELPER="$APP_PATH/Contents/Resources/agents"
test -x "$HELPER" || fail "bundled helper is not executable: $HELPER"
"$HELPER" version | grep -q "agent-observatory ${VERSION}" || fail "bundled helper version mismatch"
ok "bundled helper reports $VERSION"

status_out="$("$HELPER" status 2>&1 || true)"
grep -q "overall: installed" <<<"$status_out" || {
  printf '%s\n' "$status_out" >&2
  fail "helper status is not installed"
}
ok "helper status is installed"

health="$(curl -fsS "$API/healthz")" || fail "cannot read $API/healthz"
test "$(json_get ok "$health")" = "true" || fail "/healthz ok is not true"
test "$(json_get capturePaused "$health")" = "false" || fail "capture is paused: $(json_get capturePauseReason "$health")"
tls_failures="$(json_get clientTLSFailures "$health")"
numeric "$tls_failures" || fail "/healthz clientTLSFailures is not numeric: $tls_failures"
test "$tls_failures" = "0" || fail "clientTLSFailures is $tls_failures"
proxy_url="$(json_get proxy "$health")"
test -n "$proxy_url" || fail "/healthz proxy is empty"
ok "daemon health is clean at $proxy_url"

coverage_before="$(curl -fsS "$API/api/coverage")" || fail "cannot read $API/api/coverage before smoke"
before_bypasses="$(json_get bypasses "$coverage_before")"
numeric "$before_bypasses" || fail "/api/coverage bypasses is not numeric before smoke: $before_bypasses"
before_captures="$(json_get captures "$coverage_before")"
numeric "$before_captures" || fail "/api/coverage captures is not numeric before smoke: $before_captures"

tmp_body="$(mktemp)"
cleanup() {
  rm -f "$tmp_body"
}
trap cleanup EXIT

set +e
http_code="$(env -u HTTPS_PROXY -u HTTP_PROXY -u ALL_PROXY -u https_proxy -u http_proxy -u all_proxy \
  curl -x "$proxy_url" -sS -o "$tmp_body" -w '%{http_code}' \
    --connect-timeout 10 --max-time 30 \
    https://api.openai.com/v1/responses \
    -H 'Authorization: Bearer observatory-qa-no-key' \
    -H 'Content-Type: application/json' \
    --data '{"model":"gpt-5.5","input":"observatory installed daemon missing metadata smoke"}')"
curl_rc=$?
set -e

if [ "$curl_rc" -ne 0 ]; then
  printf '%s\n' "$(head -c 2000 "$tmp_body" 2>/dev/null || true)" >&2
  fail "missing-metadata provider-bound curl failed before provider response (curl exit $curl_rc)"
fi
case "$http_code" in
  4*) ;;
  *) printf '%s\n' "$(head -c 2000 "$tmp_body" 2>/dev/null || true)" >&2
     fail "missing-metadata provider smoke returned HTTP $http_code, want provider 4xx" ;;
esac
ok "missing-metadata provider-bound client completed with provider HTTP $http_code"

after_bypasses="$before_bypasses"
after_captures="$before_captures"
coverage_after="$coverage_before"
for _ in $(seq 1 30); do
  coverage_after="$(curl -fsS "$API/api/coverage")" || fail "cannot read $API/api/coverage after smoke"
  after_bypasses="$(json_get bypasses "$coverage_after")"
  numeric "$after_bypasses" || fail "/api/coverage bypasses is not numeric after smoke: $after_bypasses"
  after_captures="$(json_get captures "$coverage_after")"
  numeric "$after_captures" || fail "/api/coverage captures is not numeric after smoke: $after_captures"
  if [ "$after_bypasses" -gt "$before_bypasses" ]; then
    break
  fi
  sleep 0.5
done

if [ "$after_bypasses" -le "$before_bypasses" ]; then
  printf '%s\n' "$coverage_after" >&2
  fail "bypass count did not increase for missing-metadata provider-bound client"
fi
if [ "$after_captures" -ne "$before_captures" ]; then
  printf '%s\n' "$coverage_after" >&2
  fail "capture count changed for missing-metadata provider-bound client ($before_captures -> $after_captures)"
fi
grep -q 'api.openai.com' <<<"$coverage_after" || {
  printf '%s\n' "$coverage_after" >&2
  fail "recent bypasses do not include api.openai.com"
}
grep -q 'missing transparent source metadata' <<<"$coverage_after" || {
  printf '%s\n' "$coverage_after" >&2
  fail "recent bypasses do not include missing transparent source metadata"
}
ok "missing-metadata provider-bound client was recorded as pass-through coverage (captures $before_captures -> $after_captures, bypasses $before_bypasses -> $after_bypasses)"

echo "v03-installed-daemon-compat-qa: OK"
