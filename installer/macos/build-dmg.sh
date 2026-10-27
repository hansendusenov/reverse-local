#!/usr/bin/env bash
set -euo pipefail

# Native macOS .dmg Builder (uses macOS hdiutil when run on a Mac, or falls back to make_dmg.go)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DIST_DIR="$REPO_ROOT/dist"
mkdir -p "$DIST_DIR"

if command -v hdiutil >/dev/null 2>&1; then
  echo "[1/3] Building Reverse.Local.app on macOS..."
  STAGE_DIR="$(mktemp -d)"
  APP_DIR="$STAGE_DIR/Reverse.Local.app"
  mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

  (cd "$REPO_ROOT" && CGO_ENABLED=1 go build -ldflags="-s -w" -o "$APP_DIR/Contents/MacOS/reverse-local-bin" .)

  cat << 'EOF' > "$APP_DIR/Contents/MacOS/reverse-local"
#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
exec "$DIR/reverse-local-bin" -control "$@"
EOF
  chmod +x "$APP_DIR/Contents/MacOS/reverse-local" "$APP_DIR/Contents/MacOS/reverse-local-bin"

  cat << 'EOF' > "$APP_DIR/Contents/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDisplayName</key>
    <string>Reverse.Local</string>
    <key>CFBundleExecutable</key>
    <string>reverse-local</string>
    <key>CFBundleIdentifier</key>
    <string>local.reverse.daemon</string>
    <key>CFBundleName</key>
    <string>Reverse.Local</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>1.2.0</string>
    <key>CFBundleVersion</key>
    <string>1.2.0</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
</dict>
</plist>
EOF

  ln -s /Applications "$STAGE_DIR/Applications"

  echo "[2/3] Creating compressed UDZO .dmg via hdiutil..."
  DMG_OUT="$DIST_DIR/ReverseLocal-1.2.0-macOS.dmg"
  rm -f "$DMG_OUT"
  hdiutil create -volname "Reverse.Local" -srcfolder "$STAGE_DIR" -ov -format UDZO "$DMG_OUT"
  rm -rf "$STAGE_DIR"
  echo "[3/3] Created $DMG_OUT"
else
  echo "[INFO] hdiutil not found (non-macOS host); running pure-Go Universal DMG packager..."
  (cd "$REPO_ROOT" && go run installer/macos/make_dmg.go)
fi

