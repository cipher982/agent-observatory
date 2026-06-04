#!/usr/bin/env bash
# Import Developer ID signing material and provisioning profiles for GitHub
# Actions macOS release builds.
#
# Required secrets:
#   MACOS_SIGNING_CERT_P12_BASE64
#   MACOS_SIGNING_CERT_PASSWORD
#   MACOS_PROVISIONING_PROFILE_APP_BASE64
#   MACOS_PROVISIONING_PROFILE_EXT_BASE64
#
# Optional:
#   MACOS_KEYCHAIN_PASSWORD
set -euo pipefail

fail() {
  echo "ci-import-macos-signing: $*" >&2
  exit 1
}

require_env() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    fail "missing required env $name"
  fi
}

require_env MACOS_SIGNING_CERT_P12_BASE64
require_env MACOS_SIGNING_CERT_PASSWORD
require_env MACOS_PROVISIONING_PROFILE_APP_BASE64
require_env MACOS_PROVISIONING_PROFILE_EXT_BASE64

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

decode_b64() {
  /usr/bin/base64 -D
}

keychain_password="${MACOS_KEYCHAIN_PASSWORD:-$(uuidgen)}"
runner_temp="${RUNNER_TEMP:-$tmp}"
keychain_path="$runner_temp/agent-observatory-signing.keychain-db"
cert_path="$tmp/developer-id.p12"

printf "%s" "$MACOS_SIGNING_CERT_P12_BASE64" | decode_b64 > "$cert_path"

security create-keychain -p "$keychain_password" "$keychain_path"
security set-keychain-settings -lut 21600 "$keychain_path"
security unlock-keychain -p "$keychain_password" "$keychain_path"
security import "$cert_path" -k "$keychain_path" -P "$MACOS_SIGNING_CERT_PASSWORD" -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$keychain_password" "$keychain_path"
existing_keychains=()
while IFS= read -r line; do
  line="${line#${line%%[![:space:]]*}}"
  line="${line%\"}"
  line="${line#\"}"
  if [ -n "$line" ]; then
    existing_keychains+=("$line")
  fi
done < <(security list-keychains -d user)
security list-keychains -d user -s "$keychain_path" "${existing_keychains[@]}"
security default-keychain -d user -s "$keychain_path"

profiles_dir="$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"
mkdir -p "$profiles_dir"

install_profile() {
  local env_name="$1"
  local expected_name="$2"
  local tmp_profile="$tmp/$env_name.provisionprofile"
  printf "%s" "${!env_name}" | decode_b64 > "$tmp_profile"
  local uuid
  uuid="$(security cms -D -i "$tmp_profile" | plutil -extract UUID raw -o - -)"
  local name
  name="$(security cms -D -i "$tmp_profile" | plutil -extract Name raw -o - -)"
  if [ "$name" != "$expected_name" ]; then
    fail "$env_name profile name is '$name', expected '$expected_name'"
  fi
  cp "$tmp_profile" "$profiles_dir/$uuid.provisionprofile"
  echo "ci-import-macos-signing: installed provisioning profile '$name' ($uuid)"
}

install_profile MACOS_PROVISIONING_PROFILE_APP_BASE64 "Agent Observatory App DevID"
install_profile MACOS_PROVISIONING_PROFILE_EXT_BASE64 "Agent Observatory Ext DevID"

security find-identity -p codesigning -v "$keychain_path"

if [ -n "${GITHUB_ENV:-}" ]; then
  {
    echo "MACOS_SIGNING_KEYCHAIN=$keychain_path"
    echo "MACOS_KEYCHAIN_PASSWORD=$keychain_password"
  } >> "$GITHUB_ENV"
fi

echo "ci-import-macos-signing: signing material imported"
