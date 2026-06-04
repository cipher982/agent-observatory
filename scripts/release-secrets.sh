#!/usr/bin/env bash
# Inspect or populate GitHub Actions release secrets for the macOS release lane.
#
# This script deliberately splits each action into a small primitive:
#   github-doctor       report whether required GitHub Actions secrets exist
#   doctor              report which required secrets/local assets are present
#   ci-preflight        verify required release env exists inside GitHub Actions
#   set-profiles        upload the local app + extension provisioning profiles
#   set-signing-cert    upload an exported Developer ID .p12 and its password
#   set-notary-from-env upload MACOS_NOTARY_* from the current environment
set -euo pipefail

REPO="${REPO:-cipher982/agent-observatory}"
APP_PROFILE_NAME="Agent Observatory App DevID"
EXT_PROFILE_NAME="Agent Observatory Ext DevID"
APP_PROFILE_SECRET="MACOS_PROVISIONING_PROFILE_APP_BASE64"
EXT_PROFILE_SECRET="MACOS_PROVISIONING_PROFILE_EXT_BASE64"
TEAM_ID="M49WM6JSW8"
IDENTITY_PREFIX="Developer ID Application: DAVID WILLIAM ROSE"

required_secrets=(
  MACOS_SIGNING_CERT_P12_BASE64
  MACOS_SIGNING_CERT_PASSWORD
  MACOS_PROVISIONING_PROFILE_APP_BASE64
  MACOS_PROVISIONING_PROFILE_EXT_BASE64
  MACOS_NOTARY_APPLE_ID
  MACOS_NOTARY_APP_PASSWORD
  MACOS_NOTARY_TEAM_ID
)

usage() {
  cat <<'EOF'
usage: scripts/release-secrets.sh <command>

commands:
  doctor
      Check GitHub release secrets plus local signing identity/profiles.
      Local notarization auth is required only for local make notarize runs.

  github-doctor
      Check only the GitHub Actions secrets required by the CI release lane.

  ci-preflight
      Check required release environment variables inside GitHub Actions. This
      fails before Xcode/notarization work if a repository secret is missing.

  set-profiles
      Upload local provisioning profiles as GitHub Actions secrets.

  set-signing-cert <path-to-developer-id.p12>
      Upload an exported Developer ID Application .p12. Prompts for the .p12
      password without echoing, then stores both required signing secrets.

  set-notary-from-env
      Upload MACOS_NOTARY_APPLE_ID, MACOS_NOTARY_APP_PASSWORD, and
      MACOS_NOTARY_TEAM_ID from the current environment.

environment:
  REPO defaults to cipher982/agent-observatory
EOF
}

fail() {
  echo "release-secrets: $*" >&2
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

b64_file_one_line() {
  /usr/bin/base64 -i "$1" | tr -d '\n'
}

secret_names() {
  gh secret list --repo "$REPO" 2>/dev/null | awk '{print $1}'
}

secret_exists() {
  local needle="$1"
  secret_names | grep -qx "$needle"
}

profile_name() {
  local path="$1"
  security cms -D -i "$path" 2>/dev/null | plutil -extract Name raw -o - - 2>/dev/null || true
}

profile_uuid() {
  local path="$1"
  security cms -D -i "$path" 2>/dev/null | plutil -extract UUID raw -o - - 2>/dev/null || true
}

profile_team() {
  local path="$1"
  security cms -D -i "$path" 2>/dev/null | plutil -extract TeamIdentifier.0 raw -o - - 2>/dev/null || true
}

find_profile() {
  local expected_name="$1"
  local dir="$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"
  local path name
  [ -d "$dir" ] || return 1
  while IFS= read -r path; do
    name="$(profile_name "$path")"
    if [ "$name" = "$expected_name" ]; then
      printf '%s\n' "$path"
      return 0
    fi
  done < <(find "$dir" -maxdepth 1 -type f -name '*.provisionprofile' -print 2>/dev/null)
  return 1
}

set_secret_from_stdin() {
  local name="$1"
  gh secret set "$name" --repo "$REPO" >/dev/null
  echo "release-secrets: set $name"
}

set_secret_value() {
  local name="$1"
  local value="$2"
  printf '%s' "$value" | set_secret_from_stdin "$name"
}

print_secret_status() {
  local missing=0
  echo "GitHub secrets for $REPO:"
  for name in "${required_secrets[@]}"; do
    if secret_exists "$name"; then
      printf '  OK      %s\n' "$name"
    else
      printf '  MISSING %s\n' "$name"
      missing=1
    fi
  done
  return "$missing"
}

print_profile_status() {
  local label="$1"
  local expected_name="$2"
  local path
  if path="$(find_profile "$expected_name")"; then
    printf '  OK      %s: %s (%s, team %s)\n' "$label" "$path" "$(profile_uuid "$path")" "$(profile_team "$path")"
  else
    printf '  MISSING %s: %s\n' "$label" "$expected_name"
    return 1
  fi
}

doctor() {
  have_cmd gh || fail "gh is required"
  have_cmd security || fail "security is required"
  have_cmd plutil || fail "plutil is required"

  local rc=0
  if ! gh auth status >/dev/null 2>&1; then
    echo "GitHub auth: MISSING"
    rc=1
  else
    echo "GitHub auth: OK"
  fi

  print_secret_status || rc=1

  echo "Local signing assets:"
  if security find-identity -p codesigning -v | grep -q "$IDENTITY_PREFIX"; then
    printf '  OK      %s\n' "$IDENTITY_PREFIX"
  else
    printf '  MISSING %s\n' "$IDENTITY_PREFIX"
    rc=1
  fi
  print_profile_status "app profile" "$APP_PROFILE_NAME" || rc=1
  print_profile_status "extension profile" "$EXT_PROFILE_NAME" || rc=1

  echo "Local notary auth:"
  if [ -n "${NOTARY_PROFILE:-}" ]; then
    printf '  OK      NOTARY_PROFILE=%s\n' "$NOTARY_PROFILE"
  elif [ -n "${APP_STORE_CONNECT_KEY_ID:-}" ] && [ -n "${APP_STORE_CONNECT_API_KEY_P8:-}" ]; then
    printf '  OK      APP_STORE_CONNECT_KEY_ID + APP_STORE_CONNECT_API_KEY_P8\n'
  elif [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] && [ -n "${MACOS_NOTARY_APP_PASSWORD:-}" ] && [ -n "${MACOS_NOTARY_TEAM_ID:-}" ]; then
    printf '  OK      MACOS_NOTARY_APPLE_ID + MACOS_NOTARY_APP_PASSWORD + MACOS_NOTARY_TEAM_ID\n'
  else
    printf '  MISSING NOTARY_PROFILE or App Store Connect API key env or MACOS_NOTARY_* env\n'
    printf '          Local make notarize needs this; GitHub release notarization uses repo secrets above.\n'
    rc=1
  fi

  return "$rc"
}

github_doctor() {
  have_cmd gh || fail "gh is required"

  local rc=0
  if ! gh auth status >/dev/null 2>&1; then
    echo "GitHub auth: MISSING"
    rc=1
  else
    echo "GitHub auth: OK"
  fi

  print_secret_status || rc=1
  return "$rc"
}

ci_preflight() {
  local missing=0
  echo "Release environment:"
  for name in "${required_secrets[@]}"; do
    if [ -n "${!name:-}" ]; then
      printf '  OK      %s\n' "$name"
    else
      printf '  MISSING %s\n' "$name"
      missing=1
    fi
  done
  return "$missing"
}

set_profiles() {
  have_cmd gh || fail "gh is required"
  local app_profile ext_profile
  app_profile="$(find_profile "$APP_PROFILE_NAME")" || fail "could not find local profile '$APP_PROFILE_NAME'"
  ext_profile="$(find_profile "$EXT_PROFILE_NAME")" || fail "could not find local profile '$EXT_PROFILE_NAME'"
  [ "$(profile_team "$app_profile")" = "$TEAM_ID" ] || fail "app profile team id mismatch"
  [ "$(profile_team "$ext_profile")" = "$TEAM_ID" ] || fail "extension profile team id mismatch"
  b64_file_one_line "$app_profile" | set_secret_from_stdin "$APP_PROFILE_SECRET"
  b64_file_one_line "$ext_profile" | set_secret_from_stdin "$EXT_PROFILE_SECRET"
}

set_signing_cert() {
  have_cmd gh || fail "gh is required"
  local cert_path="${1:-}"
  [ -n "$cert_path" ] || fail "set-signing-cert requires a .p12 path"
  [ -f "$cert_path" ] || fail "missing .p12: $cert_path"
  local password
  read -r -s -p "P12 password: " password
  echo
  [ -n "$password" ] || fail "empty .p12 password"
  b64_file_one_line "$cert_path" | set_secret_from_stdin MACOS_SIGNING_CERT_P12_BASE64
  set_secret_value MACOS_SIGNING_CERT_PASSWORD "$password"
}

set_notary_from_env() {
  have_cmd gh || fail "gh is required"
  [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] || fail "missing MACOS_NOTARY_APPLE_ID"
  [ -n "${MACOS_NOTARY_APP_PASSWORD:-}" ] || fail "missing MACOS_NOTARY_APP_PASSWORD"
  [ -n "${MACOS_NOTARY_TEAM_ID:-}" ] || fail "missing MACOS_NOTARY_TEAM_ID"
  set_secret_value MACOS_NOTARY_APPLE_ID "$MACOS_NOTARY_APPLE_ID"
  set_secret_value MACOS_NOTARY_APP_PASSWORD "$MACOS_NOTARY_APP_PASSWORD"
  set_secret_value MACOS_NOTARY_TEAM_ID "$MACOS_NOTARY_TEAM_ID"
}

main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
    doctor) doctor ;;
    github-doctor) github_doctor ;;
    ci-preflight) ci_preflight ;;
    set-profiles) set_profiles ;;
    set-signing-cert) set_signing_cert "$@" ;;
    set-notary-from-env) set_notary_from_env ;;
    -h|--help|help|"") usage ;;
    *) usage >&2; fail "unknown command: $cmd" ;;
  esac
}

main "$@"
