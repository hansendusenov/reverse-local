#!/usr/bin/env bash
set -euo pipefail

echo "[1/3] Stopping Reverse.Local..."
sudo pkill -f "reverse-local" >/dev/null 2>&1 || true

echo "[2/3] Removing /Applications/Reverse.Local.app..."
sudo rm -rf "/Applications/Reverse.Local.app"

echo "[3/3] Done! Saved configurations in ~/Library/Application Support/reverse-local were preserved."

