#!/usr/bin/env bash
# Installed v0.3 NetworkExtension proof.
#
# This is the public-release gate that must run after notarization/stapling and
# after live capture is enabled from /Applications. It intentionally fails on a
# local unnotarized build or when an older Observatory System Extension is still
# active.
set -euo pipefail

VERSION="${VERSION:-0.3.0}"
BUILD="${BUILD:-7}"
APP_PATH="${APP_PATH:-/Applications/Agent Observatory.app}"
API="${API:-http://127.0.0.1:7878}"
BUNDLE_ID="com.github.cipher982.agentobservatory.Observatory.TransparentProxyExtension"
RUN_PROVIDER_SMOKE=1

usage() {
  cat <<EOF
usage: $(basename "$0") [--no-provider-smoke]

Environment:
  VERSION   expected app/System Extension version (default: $VERSION)
  BUILD     expected app/System Extension build (default: $BUILD)
  APP_PATH  installed app path (default: $APP_PATH)
  API       local Observatory API base URL (default: $API)
EOF
}

for arg in "$@"; do
  case "$arg" in
    --no-provider-smoke) RUN_PROVIDER_SMOKE=0 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "v03-installed-ne-proof: unknown argument $arg" >&2; usage >&2; exit 2 ;;
  esac
done

fail() {
  echo "v03-installed-ne-proof: FAIL: $*" >&2
  exit 1
}

ok() {
  echo "v03-installed-ne-proof: OK: $*"
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

test -d "$APP_PATH" || fail "installed app missing at $APP_PATH"

INFO="$APP_PATH/Contents/Info.plist"
test -f "$INFO" || fail "missing app Info.plist"

app_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$INFO" 2>/dev/null || true)"
app_build="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "$INFO" 2>/dev/null || true)"
test "$app_version" = "$VERSION" || fail "installed app version is $app_version, want $VERSION"
test "$app_build" = "$BUILD" || fail "installed app build is $app_build, want $BUILD"
ok "installed app is $VERSION/$BUILD"

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

sysext_out="$(systemextensionsctl list | grep "$BUNDLE_ID" || true)"
expected="${BUNDLE_ID} (${VERSION}/${BUILD})"
active_current="$(grep -F "$expected" <<<"$sysext_out" | grep -F "[activated enabled]" || true)"
if [ -z "$active_current" ]; then
  printf '%s\n' "$sysext_out" >&2
  fail "active System Extension is not $VERSION/$BUILD; notarize/staple, install from /Applications, enable live capture, and approve it before this proof"
fi
active_other="$(grep -F "$BUNDLE_ID" <<<"$sysext_out" | grep -F "[activated enabled]" | grep -vF "$expected" || true)"
if [ -n "$active_other" ]; then
  printf '%s\n' "$sysext_out" >&2
  fail "a different Observatory System Extension is still active"
fi
ok "System Extension $VERSION/$BUILD is activated and enabled"

health="$(curl -fsS "$API/healthz")" || fail "cannot read $API/healthz"
test "$(json_get ok "$health")" = "true" || fail "/healthz ok is not true"
test "$(json_get capturePaused "$health")" = "false" || fail "capture is paused: $(json_get capturePauseReason "$health")"
tls_failures="$(json_get clientTLSFailures "$health")"
numeric "$tls_failures" || fail "/healthz clientTLSFailures is not numeric: $tls_failures"
test "$tls_failures" = "0" || fail "clientTLSFailures is $tls_failures"
ok "daemon health is clean"

curl --noproxy '*' -fsSI https://example.com >/dev/null || fail "unrelated HTTPS traffic failed"
curl --noproxy '*' -fsSI http://example.com >/dev/null || fail "unrelated HTTP traffic failed"
ok "unrelated web traffic succeeds"

if [ "$RUN_PROVIDER_SMOKE" -eq 0 ]; then
  echo "v03-installed-ne-proof: provider smoke skipped by --no-provider-smoke"
  echo "v03-installed-ne-proof: OK"
  exit 0
fi

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
  curl --noproxy '*' -sS -o "$tmp_body" -w '%{http_code}' \
    --connect-timeout 10 --max-time 30 \
    https://api.openai.com/v1/responses \
    -H 'Authorization: Bearer observatory-qa-no-key' \
    -H 'Content-Type: application/json' \
    --data '{"model":"gpt-5.5","input":"observatory installed ne unsupported smoke"}')"
curl_rc=$?
set -e

if [ "$curl_rc" -ne 0 ]; then
  printf '%s\n' "$(head -c 2000 "$tmp_body" 2>/dev/null || true)" >&2
  fail "unsupported provider-bound curl failed before provider response (curl exit $curl_rc); this can indicate accidental TLS interception of an unknown source"
fi
case "$http_code" in
  4*) ;;
  *) printf '%s\n' "$(head -c 2000 "$tmp_body" 2>/dev/null || true)" >&2
     fail "unsupported provider smoke returned HTTP $http_code, want provider 4xx" ;;
esac
ok "unsupported provider-bound client completed with provider HTTP $http_code"

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
  fail "bypass count did not increase for unsupported provider-bound client"
fi
if [ "$after_captures" -ne "$before_captures" ]; then
  printf '%s\n' "$coverage_after" >&2
  fail "capture count changed for unsupported provider-bound client ($before_captures -> $after_captures)"
fi
grep -q 'api.openai.com' <<<"$coverage_after" || {
  printf '%s\n' "$coverage_after" >&2
  fail "recent bypasses do not include api.openai.com"
}
ok "unsupported provider-bound client was recorded as pass-through coverage (captures $before_captures -> $after_captures, bypasses $before_bypasses -> $after_bypasses)"

echo "v03-installed-ne-proof: OK"
