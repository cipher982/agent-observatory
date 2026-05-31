#!/usr/bin/env bash
# Verifies the public macOS artifacts created by `make release`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-0.1.0}"
DIST="$ROOT/dist"
APP_NAME="Agent Observatory.app"
DMG_NAME="Agent-Observatory-${VERSION}-macOS.dmg"
ZIP_NAME="Agent-Observatory-${VERSION}-macOS.zip"

fail() {
  echo "release-qa: $*" >&2
  exit 1
}

require_path() {
  test -e "$1" || fail "missing $1"
}

require_path "$DIST/$DMG_NAME"
require_path "$DIST/$ZIP_NAME"
require_path "$DIST/$APP_NAME"
require_path "$DIST/agents"
require_path "$DIST/SHA256SUMS"

if find "$DIST" -maxdepth 1 -name '*\\*' | grep -q .; then
  fail "dist contains a literal backslash path"
fi

HELPER="$DIST/$APP_NAME/Contents/Resources/agents"
test -x "$HELPER" || fail "bundled helper is not executable: $HELPER"

tmp_home="$(mktemp -d)"
status_out="$(env -i HOME="$tmp_home" "$HELPER" status 2>&1 || true)"
rm -rf "$tmp_home"
grep -q "Agent Observatory .* install status" <<<"$status_out" || fail "helper status did not run from a fresh env"
grep -q "overall: not fully installed" <<<"$status_out" || fail "fresh HOME status was not the expected clean state"

mount_dir="$(mktemp -d)"
attached=0
cleanup_mount() {
  if [ "$attached" -eq 1 ]; then
    hdiutil detach "$mount_dir" >/dev/null || true
  fi
  rm -rf "$mount_dir"
}
trap cleanup_mount EXIT

hdiutil attach -nobrowse -readonly -mountpoint "$mount_dir" "$DIST/$DMG_NAME" >/dev/null
attached=1
test -d "$mount_dir/$APP_NAME" || fail "DMG missing $APP_NAME"
test -L "$mount_dir/Applications" || fail "DMG missing Applications symlink"
test "$(readlink "$mount_dir/Applications")" = "/Applications" || fail "DMG Applications symlink points to $(readlink "$mount_dir/Applications")"
test -x "$mount_dir/$APP_NAME/Contents/Resources/agents" || fail "DMG app missing executable bundled helper"
test -f "$mount_dir/.background/background.png" || fail "DMG missing branded Finder background"
test -f "$mount_dir/.DS_Store" || fail "DMG missing Finder window layout metadata"
hdiutil detach "$mount_dir" >/dev/null
attached=0

zip_root="$(mktemp -d)"
ditto -x -k "$DIST/$ZIP_NAME" "$zip_root"
test -d "$zip_root/$APP_NAME" || fail "zip missing $APP_NAME"
test -x "$zip_root/$APP_NAME/Contents/Resources/agents" || fail "zip app missing executable bundled helper"
rm -rf "$zip_root"

(cd "$DIST" && shasum -a 256 -c SHA256SUMS >/dev/null)

echo "release-qa: OK"
