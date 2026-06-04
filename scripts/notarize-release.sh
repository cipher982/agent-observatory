#!/usr/bin/env bash
# Notarize and staple the current dist/ release artifacts, then rebuild checksums.
#
# Requires one notarization auth mode:
#   NOTARY_PROFILE=<xcrun notarytool keychain profile>
#   APP_STORE_CONNECT_KEY_ID=<key id>
#   APP_STORE_CONNECT_API_KEY_P8=<raw .p8 contents or path to .p8 file>
#   APP_STORE_CONNECT_ISSUER_ID=<issuer uuid>   # omit for Individual API keys
#   MACOS_NOTARY_APPLE_ID=<apple id>
#   MACOS_NOTARY_APP_PASSWORD=<app-specific password>
#   MACOS_NOTARY_TEAM_ID=<team id>
#
# Optional:
#   DMG_STYLE=headless|polished       default: headless
#   DMG_CODESIGN_IDENTITY=<identity>  passed through to scripts/make-dmg.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-0.3.0}"
DIST="$ROOT/dist"
APP_NAME="Agent Observatory.app"
DMG_NAME="Agent-Observatory-${VERSION}-macOS.dmg"
ZIP_NAME="Agent-Observatory-${VERSION}-macOS.zip"
NOTARY_PROFILE="${NOTARY_PROFILE:-}"
DMG_STYLE="${DMG_STYLE:-headless}"

APP_PATH="$DIST/$APP_NAME"
DMG_PATH="$DIST/$DMG_NAME"
ZIP_PATH="$DIST/$ZIP_NAME"
HELPER_PATH="$APP_PATH/Contents/Resources/agents"

fail() {
  echo "notarize-release: $*" >&2
  exit 1
}

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

notary_auth_args=()
if [ -n "$NOTARY_PROFILE" ]; then
  notary_auth_args=(--keychain-profile "$NOTARY_PROFILE")
elif [ -n "${APP_STORE_CONNECT_KEY_ID:-}" ] && [ -n "${APP_STORE_CONNECT_API_KEY_P8:-}" ]; then
  key_path="$APP_STORE_CONNECT_API_KEY_P8"
  if [ ! -f "$key_path" ]; then
    case "$key_path" in
      /*|./*|../*|~/*)
        fail "App Store Connect API key file not found: $key_path"
        ;;
    esac
    mkdir -p "$tmp"
    key_path="$tmp/app-store-connect-key.p8"
    printf "%s\n" "$APP_STORE_CONNECT_API_KEY_P8" > "$key_path"
    chmod 600 "$key_path"
  fi
  notary_auth_args=(--key "$key_path" --key-id "$APP_STORE_CONNECT_KEY_ID")
  if [ -n "${APP_STORE_CONNECT_ISSUER_ID:-}" ]; then
    notary_auth_args+=(--issuer "$APP_STORE_CONNECT_ISSUER_ID")
  fi
elif [ -n "${MACOS_NOTARY_APPLE_ID:-}" ] && [ -n "${MACOS_NOTARY_APP_PASSWORD:-}" ] && [ -n "${MACOS_NOTARY_TEAM_ID:-}" ]; then
  notary_auth_args=(
    --apple-id "$MACOS_NOTARY_APPLE_ID"
    --password "$MACOS_NOTARY_APP_PASSWORD"
    --team-id "$MACOS_NOTARY_TEAM_ID"
  )
else
  fail "set NOTARY_PROFILE, APP_STORE_CONNECT_KEY_ID + APP_STORE_CONNECT_API_KEY_P8, or MACOS_NOTARY_APPLE_ID + MACOS_NOTARY_APP_PASSWORD + MACOS_NOTARY_TEAM_ID"
fi

test -d "$APP_PATH" || fail "missing $APP_PATH (run make release first)"
test -x "$HELPER_PATH" || fail "missing executable bundled helper at $HELPER_PATH"
codesign --verify --deep --strict "$APP_PATH" || fail "app codesign verification failed"

if [ -z "${DMG_CODESIGN_IDENTITY:-}" ]; then
  codesign_info="$(codesign -dv --verbose=4 "$APP_PATH" 2>&1)"
  DMG_CODESIGN_IDENTITY="$(awk -F= '/^Authority=Developer ID Application:/ { print $2; exit }' <<<"$codesign_info")"
fi
test -n "${DMG_CODESIGN_IDENTITY:-}" || fail "could not infer Developer ID identity for DMG signing"

echo "notarize-release: submitting app bundle via temporary zip"
ditto -c -k --keepParent "$APP_PATH" "$tmp/app-notary.zip"
xcrun notarytool submit "$tmp/app-notary.zip" "${notary_auth_args[@]}" --wait
xcrun stapler staple "$APP_PATH"
xcrun stapler validate "$APP_PATH"

echo "notarize-release: rebuilding zip with stapled app"
rm -f "$ZIP_PATH"
(cd "$DIST" && ditto -c -k --keepParent "$APP_NAME" "$ZIP_NAME")

echo "notarize-release: rebuilding $DMG_STYLE DMG with stapled app"
DMG_STYLE="$DMG_STYLE" DMG_CODESIGN_IDENTITY="${DMG_CODESIGN_IDENTITY:-}" \
  "$ROOT/scripts/make-dmg.sh" "$APP_PATH" "$DMG_PATH" "Agent Observatory"
codesign --verify --strict "$DMG_PATH" || fail "DMG codesign verification failed"

echo "notarize-release: submitting DMG"
xcrun notarytool submit "$DMG_PATH" "${notary_auth_args[@]}" --wait
xcrun stapler staple "$DMG_PATH"
xcrun stapler validate "$DMG_PATH"

echo "notarize-release: refreshing checksums"
(cd "$DIST" && shasum -a 256 "$DMG_NAME" "$ZIP_NAME" agents > SHA256SUMS)

"$ROOT/scripts/release-qa.sh" --notarized
