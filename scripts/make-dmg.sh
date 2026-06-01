#!/usr/bin/env bash
# Build a macOS drag-install DMG.
#
# Default mode is headless: it never runs Finder AppleScript, so release builds
# do not steal GUI focus. Set DMG_STYLE=polished for the old Finder-prettified
# layout (custom background/icon positions), which is intentionally explicit.
set -euo pipefail

APP_PATH="${1:?app bundle path required}"
OUT_DMG="${2:?output dmg path required}"
VOL_NAME="${3:-Agent Observatory}"
DMG_STYLE="${DMG_STYLE:-headless}" # headless | polished

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_BUNDLE_NAME="$(basename "$APP_PATH")"
ICON="$ROOT/app/Observatory/Assets.xcassets/AppIcon.appiconset/icon_512x512@2x.png"
FONT_REGULAR="/System/Library/Fonts/SFNS.ttf"
FONT_BOLD="/System/Library/Fonts/SFNS.ttf"

command -v magick >/dev/null || { echo "make-dmg: ImageMagick 'magick' is required" >&2; exit 1; }
command -v create-dmg >/dev/null || { echo "make-dmg: create-dmg is required (brew install create-dmg)" >&2; exit 1; }

case "$DMG_STYLE" in
  headless|polished) ;;
  *) echo "make-dmg: DMG_STYLE must be headless or polished (got $DMG_STYLE)" >&2; exit 2 ;;
esac

tmp="$(mktemp -d)"
staging="$tmp/staging"
bg="$staging/.background/background.png"

cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

mkdir -p "$staging/.background" "$(dirname "$OUT_DMG")"
cp -R "$APP_PATH" "$staging/$APP_BUNDLE_NAME"

for suffix in "" " 1" " 2" " 3" " 4" " 5"; do
  existing="/Volumes/$VOL_NAME$suffix"
  if mount | grep -q " on $existing "; then
    hdiutil detach "$existing" >/dev/null || true
  fi
done

magick -size 760x480 canvas:'#111827' \
  \( -size 760x480 gradient:'#172033-#030712' \) -compose over -composite \
  \( -size 760x480 xc:none -fill 'rgba(34,211,238,0.24)' -draw 'circle 162,104 162,20' -blur 0x36 \) -compose over -composite \
  \( -size 760x480 xc:none -fill 'rgba(168,85,247,0.24)' -draw 'circle 620,390 620,300' -blur 0x46 \) -compose over -composite \
  \( "$ICON" -resize 96x96 \) -geometry +332+54 -compose over -composite \
  -fill white -font "$FONT_BOLD" -pointsize 34 -gravity north -annotate +0+166 'Agent Observatory' \
  -fill '#cbd5e1' -font "$FONT_REGULAR" -pointsize 17 -gravity north -annotate +0+211 'Drag the app to Applications' \
  -stroke '#22d3ee' -strokewidth 4 -fill none -draw 'line 312,312 448,312' \
  -stroke '#22d3ee' -strokewidth 4 -fill '#22d3ee' -draw 'polygon 448,312 428,300 428,324' \
  -stroke none -fill 'rgba(255,255,255,0.70)' -draw 'roundrectangle 142,371 302,397 10,10' \
  -stroke none -fill 'rgba(255,255,255,0.70)' -draw 'roundrectangle 484,371 592,397 10,10' \
  -fill '#cbd5e1' -font "$FONT_REGULAR" -pointsize 13 -gravity north -annotate +0+405 'Live capture is optional and set up inside the app' \
  "$bg"

extra_args=(--hdiutil-quiet)
if [ "$DMG_STYLE" = "headless" ]; then
  # create-dmg's Finder-prettifying AppleScript is the focus-stealing step. Skip
  # it by default; the DMG remains valid, mountable, signed/notarizable, and keeps
  # the Applications symlink, but without custom Finder window metadata.
  extra_args+=(--skip-jenkins)
fi
if [ -n "${DMG_CODESIGN_IDENTITY:-}" ]; then
  extra_args+=(--codesign "$DMG_CODESIGN_IDENTITY")
fi

rm -f "$OUT_DMG"
create-dmg \
  "${extra_args[@]}" \
  --volname "$VOL_NAME" \
  --background "$bg" \
  --window-pos 100 100 \
  --window-size 760 480 \
  --text-size 13 \
  --icon-size 96 \
  --icon "$APP_BUNDLE_NAME" 222 312 \
  --hide-extension "$APP_BUNDLE_NAME" \
  --app-drop-link 538 312 \
  --format UDZO \
  --filesystem HFS+ \
  --no-internet-enable \
  "$OUT_DMG" \
  "$staging" >/dev/null

echo "created ($DMG_STYLE): $OUT_DMG"
