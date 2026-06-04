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
#   infisical-doctor    report whether Infisical has the release secrets
#   infisical-init      create the scoped Infisical release folder
#   github-from-infisical
#                       sync GitHub Actions release secrets from Infisical
set -euo pipefail

REPO="${REPO:-cipher982/agent-observatory}"
APP_PROFILE_NAME="Agent Observatory App DevID"
EXT_PROFILE_NAME="Agent Observatory Ext DevID"
APP_PROFILE_SECRET="MACOS_PROVISIONING_PROFILE_APP_BASE64"
EXT_PROFILE_SECRET="MACOS_PROVISIONING_PROFILE_EXT_BASE64"
TEAM_ID="M49WM6JSW8"
IDENTITY_PREFIX="Developer ID Application: DAVID WILLIAM ROSE"
INFISICAL_RELEASE_ENV="${INFISICAL_RELEASE_ENV:-prod}"
INFISICAL_RELEASE_PATH="${INFISICAL_RELEASE_PATH:-/agent-observatory/release}"
INFISICAL_RELEASE_PROJECT_ID="${INFISICAL_RELEASE_PROJECT_ID:-${INFISICAL_PROJECT_ID:-}}"

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

  infisical-doctor
      Check whether Infisical has all release secrets. Requires
      INFISICAL_RELEASE_PROJECT_ID or INFISICAL_PROJECT_ID.

  infisical-init
      Create INFISICAL_RELEASE_PATH if needed.

  set-infisical-profiles
      Upload local provisioning profiles to Infisical.

  set-infisical-signing-cert <path-to-developer-id.p12>
      Upload the Developer ID .p12 and its password to Infisical.

  set-infisical-notary-from-env
      Upload MACOS_NOTARY_APPLE_ID, MACOS_NOTARY_APP_PASSWORD, and
      MACOS_NOTARY_TEAM_ID from the current environment to Infisical.

  github-from-infisical
      Upload all required GitHub Actions release secrets from Infisical.

environment:
  REPO defaults to cipher982/agent-observatory
  INFISICAL_RELEASE_PROJECT_ID or INFISICAL_PROJECT_ID selects the Infisical project
  INFISICAL_RELEASE_ENV defaults to prod
  INFISICAL_RELEASE_PATH defaults to /agent-observatory/release
EOF
}

fail() {
  echo "release-secrets: $*" >&2
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

require_infisical_context() {
  have_cmd infisical || fail "infisical is required"
  have_cmd jq || fail "jq is required"
  [ -n "$INFISICAL_RELEASE_PROJECT_ID" ] || fail "set INFISICAL_RELEASE_PROJECT_ID or INFISICAL_PROJECT_ID"
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

dotenv_quote() {
  local value="$1"
  value="${value//\'/\'\\\'\'}"
  printf "'%s'" "$value"
}

write_env_line() {
  local name="$1"
  local value="$2"
  printf '%s=' "$name"
  dotenv_quote "$value"
  printf '\n'
}

infisical_set_file() {
  local file="$1"
  require_infisical_context
  infisical secrets set \
    --projectId "$INFISICAL_RELEASE_PROJECT_ID" \
    --env "$INFISICAL_RELEASE_ENV" \
    --path "$INFISICAL_RELEASE_PATH" \
    --file "$file" \
    --silent >/dev/null
}

infisical_json() {
  require_infisical_context
  infisical secrets \
    --projectId "$INFISICAL_RELEASE_PROJECT_ID" \
    --env "$INFISICAL_RELEASE_ENV" \
    --path "$INFISICAL_RELEASE_PATH" \
    --output json \
    --silent
}

infisical_folder_exists() {
  local parent="$1"
  local name="$2"
  infisical secrets folders get \
    --projectId "$INFISICAL_RELEASE_PROJECT_ID" \
    --env "$INFISICAL_RELEASE_ENV" \
    --path "$parent" \
    --output json \
    --silent \
    | jq -e --arg name "$name" '(. // [])[] | select(.folderName == $name)' >/dev/null
}

infisical_create_folder_if_missing() {
  local parent="$1"
  local name="$2"
  if infisical_folder_exists "$parent" "$name"; then
    return 0
  fi
  infisical secrets folders create \
    --projectId "$INFISICAL_RELEASE_PROJECT_ID" \
    --env "$INFISICAL_RELEASE_ENV" \
    --path "$parent" \
    --name "$name" \
    --silent >/dev/null
}

infisical_init() {
  require_infisical_context
  local trimmed parent part
  trimmed="${INFISICAL_RELEASE_PATH#/}"
  trimmed="${trimmed%/}"
  [ -n "$trimmed" ] || return 0
  parent="/"
  IFS='/' read -r -a parts <<<"$trimmed"
  for part in "${parts[@]}"; do
    [ -n "$part" ] || continue
    infisical_create_folder_if_missing "$parent" "$part"
    if [ "$parent" = "/" ]; then
      parent="/$part"
    else
      parent="$parent/$part"
    fi
  done
  echo "release-secrets: Infisical folder ready: $INFISICAL_RELEASE_PATH"
}

infisical_secret_value() {
  local name="$1"
  infisical_json | jq -er --arg name "$name" '
    (. // [])[] | select(.secretKey == $name) | .secretValue
  '
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

infisical_doctor() {
  require_infisical_context
  local secrets rc=0
  secrets="$(infisical_json)"
  echo "Infisical release secrets:"
  echo "  project: $INFISICAL_RELEASE_PROJECT_ID"
  echo "  env:     $INFISICAL_RELEASE_ENV"
  echo "  path:    $INFISICAL_RELEASE_PATH"
  for name in "${required_secrets[@]}"; do
    if jq -e --arg name "$name" '(. // [])[] | select(.secretKey == $name)' >/dev/null <<<"$secrets"; then
      printf '  OK      %s\n' "$name"
    else
      printf '  MISSING %s\n' "$name"
      rc=1
    fi
  done
  return "$rc"
}

set_infisical_profiles() {
  local app_profile ext_profile tmp
  app_profile="$(find_profile "$APP_PROFILE_NAME")" || fail "could not find local profile '$APP_PROFILE_NAME'"
  ext_profile="$(find_profile "$EXT_PROFILE_NAME")" || fail "could not find local profile '$EXT_PROFILE_NAME'"
  [ "$(profile_team "$app_profile")" = "$TEAM_ID" ] || fail "app profile team id mismatch"
  [ "$(profile_team "$ext_profile")" = "$TEAM_ID" ] || fail "extension profile team id mismatch"

  tmp="$(mktemp)"
  chmod 600 "$tmp"
  trap 'rm -f "${tmp:-}"' RETURN
  write_env_line "$APP_PROFILE_SECRET" "$(b64_file_one_line "$app_profile")" >>"$tmp"
  write_env_line "$EXT_PROFILE_SECRET" "$(b64_file_one_line "$ext_profile")" >>"$tmp"
  infisical_set_file "$tmp"
  echo "release-secrets: set Infisical provisioning profile secrets"
}

set_infisical_signing_cert() {
  local cert_path="${1:-}"
  [ -n "$cert_path" ] || fail "set-infisical-signing-cert requires a .p12 path"
  [ -f "$cert_path" ] || fail "missing .p12: $cert_path"
  local password tmp
  read -r -s -p "P12 password: " password
  echo
  [ -n "$password" ] || fail "empty .p12 password"

  tmp="$(mktemp)"
  chmod 600 "$tmp"
  trap 'rm -f "${tmp:-}"' RETURN
  write_env_line MACOS_SIGNING_CERT_P12_BASE64 "$(b64_file_one_line "$cert_path")" >>"$tmp"
  write_env_line MACOS_SIGNING_CERT_PASSWORD "$password" >>"$tmp"
  infisical_set_file "$tmp"
  echo "release-secrets: set Infisical signing certificate secrets"
}

set_infisical_notary_from_env() {
  [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] || fail "missing MACOS_NOTARY_APPLE_ID"
  [ -n "${MACOS_NOTARY_APP_PASSWORD:-}" ] || fail "missing MACOS_NOTARY_APP_PASSWORD"
  [ -n "${MACOS_NOTARY_TEAM_ID:-}" ] || fail "missing MACOS_NOTARY_TEAM_ID"
  local tmp
  tmp="$(mktemp)"
  chmod 600 "$tmp"
  trap 'rm -f "${tmp:-}"' RETURN
  write_env_line MACOS_NOTARY_APPLE_ID "$MACOS_NOTARY_APPLE_ID" >>"$tmp"
  write_env_line MACOS_NOTARY_APP_PASSWORD "$MACOS_NOTARY_APP_PASSWORD" >>"$tmp"
  write_env_line MACOS_NOTARY_TEAM_ID "$MACOS_NOTARY_TEAM_ID" >>"$tmp"
  infisical_set_file "$tmp"
  echo "release-secrets: set Infisical notary secrets"
}

github_from_infisical() {
  have_cmd gh || fail "gh is required"
  require_infisical_context

  local name value
  for name in "${required_secrets[@]}"; do
    if ! value="$(infisical_secret_value "$name")"; then
      fail "missing Infisical secret: $name"
    fi
    set_secret_value "$name" "$value"
  done
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
    infisical-doctor) infisical_doctor ;;
    infisical-init) infisical_init ;;
    set-infisical-profiles) set_infisical_profiles ;;
    set-infisical-signing-cert) set_infisical_signing_cert "$@" ;;
    set-infisical-notary-from-env) set_infisical_notary_from_env ;;
    github-from-infisical) github_from_infisical ;;
    -h|--help|help|"") usage ;;
    *) usage >&2; fail "unknown command: $cmd" ;;
  esac
}

main "$@"
