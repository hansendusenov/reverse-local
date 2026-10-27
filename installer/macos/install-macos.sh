#!/usr/bin/env bash
set -euo pipefail

echo "========================================================================"
echo "  Reverse.Local // macOS Application Bundle & Daemon Installer"
echo "  Local Reverse Proxy, Native SSH Tunnel (-L -N) & LAN DNS Broadcaster"
echo "========================================================================"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
APP_BUNDLE="/Applications/Reverse.Local.app"
CONTENTS_DIR="$APP_BUNDLE/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"
RESOURCES_DIR="$CONTENTS_DIR/Resources"

# 1. Build or locate binary
BIN_SRC="$REPO_ROOT/reverse-local"
if [ ! -f "$BIN_SRC" ]; then
  if command -v go >/dev/null 2>&1; then
    echo "[1/4] Building reverse-local binary for macOS..."
    (cd "$REPO_ROOT" && go build -ldflags="-s -w" -o "$BIN_SRC" .)
  else
    echo "[ERROR] Binary not found at $BIN_SRC and Go compiler is not installed."
    exit 1
  fi
fi

# 2. Request sudo to install into /Applications
echo "[2/4] Creating /Applications/Reverse.Local.app bundle..."
sudo mkdir -p "$MACOS_DIR" "$RESOURCES_DIR"
sudo cp "$BIN_SRC" "$MACOS_DIR/reverse-local-bin"
sudo chmod +x "$MACOS_DIR/reverse-local-bin"

# Create launcher wrapper that opens with Administrator prompt when clicked from Finder
cat << 'EOF' | sudo tee "$MACOS_DIR/reverse-local" >/dev/null
#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
exec "$DIR/reverse-local-bin" -control "$@"
EOF
sudo chmod +x "$MACOS_DIR/reverse-local"

# 3. Write Info.plist
echo "[3/4] Writing Contents/Info.plist..."
cat << 'EOF' | sudo tee "$CONTENTS_DIR/Info.plist" >/dev/null
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

echo "[4/4] Installation complete!"
echo "  App Bundle : /Applications/Reverse.Local.app"
echo "  Config Dir : ~/Library/Application Support/reverse-local/"
echo "  Workbench  : https://reverse.local"

