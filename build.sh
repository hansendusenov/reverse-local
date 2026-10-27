#!/usr/bin/env bash
set -e

echo "Reverse.Local Build Script (Linux & macOS)"

if ! command -v go &> /dev/null; then
    echo "[ERROR] Go compiler not found. Please install Go 1.22+ first."
    exit 1
fi

echo "[INFO] Running unit test suite..."
go test ./...

echo "[INFO] Building reverse-local binary..."
go build -ldflags="-s -w" -o reverse-local .

echo "[SUCCESS] Built binary: ./reverse-local"
echo ""
echo "Run with root privileges:"
echo "  sudo ./reverse-local -headless"
