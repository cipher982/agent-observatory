#!/usr/bin/env bash
# Notarize and staple the current dist/ release artifacts, then rebuild checksums.
#
# Requires:
#   NOTARY_PROFILE=<xcrun notarytool keychain profile>
#
# Optional:
#   DMG_STYLE=headless|polished       default: headless
#   DMG_CODESIGN_IDENTITY=<identity>  passed through to scripts/make-dmg.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-0.2.0}"
DIST="$ROOT/dist"
APP_NAME="Agent Observatory.app"
DMG_NAME="Agent-Observatory-${VERSION}-macOS.dmg"
ZIP_NAME="Agent-Observatory-${VERSION}-macOS.zip"
NOTARY_PROFILE="${NOTARY_PROFILE:?set NOTARY_PROFILE=<notarytool keychain profile>}"
DMG_STYLE="${DMG_STYLE:-headless}"

APP_PATH="$DIST/$APP_NAME"
DMG_PATH="$DIST/$DMG_NAME"
ZIP_PATH="$DIST/$ZIP_NAME"
HELPER_PATH="$APP_PATH/Contents/Resources/agents"

fail() {
  echo "notarize-release: $*" >&2
  exit 1
}

test -d "$APP_PATH" || fail "missing $APP_PATH (run make release first)"
test -x "$HELPER_PATH" || fail "missing executable bundled helper at $HELPER_PATH"
codesign --verify --deep --strict "$APP_PATH" || fail "app codesign verification failed"

tmp="$(mktemp -d)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

echo "notarize-release: submitting app bundle via temporary zip"
ditto -c -k --keepParent "$APP_PATH" "$tmp/app-notary.zip"
xcrun notarytool submit "$tmp/app-notary.zip" --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple "$APP_PATH"
xcrun stapler validate "$APP_PATH"

echo "notarize-release: rebuilding zip with stapled app"
rm -f "$ZIP_PATH"
(cd "$DIST" && ditto -c -k --keepParent "$APP_NAME" "$ZIP_NAME")

echo "notarize-release: rebuilding $DMG_STYLE DMG with stapled app"
DMG_STYLE="$DMG_STYLE" DMG_CODESIGN_IDENTITY="${DMG_CODESIGN_IDENTITY:-}" \
  "$ROOT/scripts/make-dmg.sh" "$APP_PATH" "$DMG_PATH" "Agent Observatory"

echo "notarize-release: submitting DMG"
xcrun notarytool submit "$DMG_PATH" --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple "$DMG_PATH"
xcrun stapler validate "$DMG_PATH"

echo "notarize-release: refreshing checksums"
(cd "$DIST" && shasum -a 256 "$DMG_NAME" "$ZIP_NAME" agents > SHA256SUMS)

"$ROOT/scripts/release-qa.sh" --notarized
