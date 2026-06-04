#!/usr/bin/env bash
# Stage macOS release secrets from this Mac into Infisical, then mirror them to
# GitHub Actions. This is intentionally the human-gated ceremony: approve
# 1Password/keychain prompts here, then later release steps should be noninteractive.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE_SECRETS="$REPO_ROOT/scripts/release-secrets.sh"
INFISICAL_RELEASE_PROJECT_ID="${INFISICAL_RELEASE_PROJECT_ID:-${INFISICAL_PROJECT_ID:-}}"
INFISICAL_RELEASE_ENV="${INFISICAL_RELEASE_ENV:-prod}"
INFISICAL_RELEASE_PATH="${INFISICAL_RELEASE_PATH:-/agent-observatory/release}"
TEAM_ID="${MACOS_NOTARY_TEAM_ID:-M49WM6JSW8}"
IDENTITY_FINGERPRINT="${MACOS_SIGNING_IDENTITY_FINGERPRINT:-5EE7A7417CA93F2F45C55BB5D9CFB3EA9953DE78}"
IDENTITY_NAME="${MACOS_SIGNING_IDENTITY_NAME:-Developer ID Application: DAVID WILLIAM ROSE (M49WM6JSW8)}"
OP_NOTARY_ITEM="${OP_NOTARY_ITEM:-}"

fail() {
  echo "stage-release-secrets: $*" >&2
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

export_common_env() {
  export INFISICAL_RELEASE_PROJECT_ID
  export INFISICAL_RELEASE_ENV
  export INFISICAL_RELEASE_PATH
}

print_plan() {
  cat <<EOF
stage-release-secrets: prompt plan
  1. Validate local tools and Infisical/GitHub auth.
  2. Read notary credentials from current env or one 1Password item.
  3. Export the local Developer ID identity from Keychain.
  4. Verify the exported .p12 contains $IDENTITY_FINGERPRINT.
  5. Store release secrets in Infisical at $INFISICAL_RELEASE_PATH.
  6. Mirror Infisical release secrets into GitHub Actions.

Expected prompts are front-loaded:
  - 1Password unlock/read prompt, only if OP_NOTARY_ITEM is set and not already unlocked.
  - macOS Keychain private-key export prompt for "$IDENTITY_NAME".
EOF
}

require_tools() {
  have_cmd gh || fail "missing gh"
  have_cmd infisical || fail "missing infisical"
  have_cmd jq || fail "missing jq"
  have_cmd security || fail "missing security"
  have_cmd xcrun || fail "missing xcrun"
  have_cmd uuidgen || fail "missing uuidgen"
  [ -n "$INFISICAL_RELEASE_PROJECT_ID" ] || fail "set INFISICAL_RELEASE_PROJECT_ID or INFISICAL_PROJECT_ID"
  gh auth status >/dev/null 2>&1 || fail "gh is not authenticated"
}

resolve_notary_env() {
  if [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] &&
     [ -n "${MACOS_NOTARY_APP_PASSWORD:-}" ] &&
     [ -n "${MACOS_NOTARY_TEAM_ID:-}" ]; then
    echo "stage-release-secrets: using MACOS_NOTARY_* from environment"
    return 0
  fi

  [ -n "$OP_NOTARY_ITEM" ] || fail "set MACOS_NOTARY_* env or OP_NOTARY_ITEM=<1Password item id/name>"
  have_cmd op || fail "missing op for OP_NOTARY_ITEM"

  local item_json username password
  echo "stage-release-secrets: reading one 1Password item for notary credentials"
  item_json="$(op item get "$OP_NOTARY_ITEM" --format json)"
  username="$(jq -er '.fields[] | select(.purpose == "USERNAME" or .id == "username") | .value' <<<"$item_json" | head -n 1)"
  password="$(jq -er '.fields[] | select(.purpose == "PASSWORD" or .id == "password") | .value' <<<"$item_json" | head -n 1)"
  [ -n "$username" ] || fail "1Password item did not contain a username"
  [ -n "$password" ] || fail "1Password item did not contain a password"

  export MACOS_NOTARY_APPLE_ID="$username"
  export MACOS_NOTARY_APP_PASSWORD="$password"
  export MACOS_NOTARY_TEAM_ID="$TEAM_ID"
}

validate_notary_credentials() {
  echo "stage-release-secrets: validating notary credentials"
  xcrun notarytool history \
    --apple-id "$MACOS_NOTARY_APPLE_ID" \
    --password "$MACOS_NOTARY_APP_PASSWORD" \
    --team-id "$MACOS_NOTARY_TEAM_ID" >/dev/null
  echo "stage-release-secrets: notary credentials accepted"
}

export_developer_id_p12() {
  local tmp p12 p12_pass
  tmp="$(mktemp -d)"
  chmod 700 "$tmp"
  p12="$tmp/developer-id.p12"
  p12_pass="$(uuidgen)"

  echo "stage-release-secrets: exporting Developer ID identity from login keychain" >&2
  echo "stage-release-secrets: approve the macOS private-key export prompt now" >&2
  security export \
    -k "$HOME/Library/Keychains/login.keychain-db" \
    -t identities \
    -f pkcs12 \
    -P "$p12_pass" \
    -o "$p12" >/dev/null

  verify_p12 "$p12" "$p12_pass"
  printf '%s\n%s\n%s\n' "$tmp" "$p12" "$p12_pass"
}

verify_p12() {
  local p12="$1"
  local p12_pass="$2"
  local verify_keychain verify_pass
  verify_keychain="$(mktemp -u "${TMPDIR:-/tmp}/agent-observatory-p12-verify.XXXXXX.keychain-db")"
  verify_pass="$(uuidgen)"
  security create-keychain -p "$verify_pass" "$verify_keychain"
  security unlock-keychain -p "$verify_pass" "$verify_keychain"
  security import "$p12" \
    -k "$verify_keychain" \
    -P "$p12_pass" \
    -T /usr/bin/codesign \
    -T /usr/bin/security >/dev/null
  if ! security find-identity -p codesigning -v "$verify_keychain" | grep -q "$IDENTITY_FINGERPRINT"; then
    security delete-keychain "$verify_keychain" >/dev/null 2>&1 || true
    fail "exported .p12 does not contain expected Developer ID fingerprint $IDENTITY_FINGERPRINT"
  fi
  security delete-keychain "$verify_keychain" >/dev/null 2>&1 || true
  echo "stage-release-secrets: verified exported Developer ID fingerprint" >&2
}

set_infisical_signing_cert() {
  local p12="$1"
  local p12_pass="$2"
  printf '%s\n' "$p12_pass" | "$RELEASE_SECRETS" set-infisical-signing-cert "$p12"
}

main() {
  cd "$REPO_ROOT"
  export_common_env
  print_plan
  require_tools
  "$RELEASE_SECRETS" infisical-init
  resolve_notary_env
  validate_notary_credentials

  local export_info tmp p12 p12_pass
  export_info="$(export_developer_id_p12)"
  tmp="$(sed -n '1p' <<<"$export_info")"
  p12="$(sed -n '2p' <<<"$export_info")"
  p12_pass="$(sed -n '3p' <<<"$export_info")"
  trap 'rm -rf "${tmp:-}"' EXIT

  "$RELEASE_SECRETS" set-infisical-profiles
  set_infisical_signing_cert "$p12" "$p12_pass"
  "$RELEASE_SECRETS" set-infisical-notary-from-env
  "$RELEASE_SECRETS" infisical-doctor
  "$RELEASE_SECRETS" github-from-infisical
  "$RELEASE_SECRETS" github-doctor
}

main "$@"
