#!/usr/bin/env bash
set -e

echo "========================================================================"
echo "  Reverse.Local - Build Script (Linux & macOS)"
echo "========================================================================"

if ! command -v go &> /dev/null; then
    echo "[ERROR] Go compiler tidak ditemukan! Silakan install Go 1.22+ terlebih dahulu."
    exit 1
fi

echo "[INFO] Menjalankan test suite..."
go test ./...

echo "[INFO] Membangun binary reverse-local..."
go build -ldflags="-s -w" -o reverse-local .

echo "[SUCCESS] Binary berhasil dibuat: ./reverse-local"
echo ""
echo "Cara menjalankan dengan hak akses root:"
echo "  sudo ./reverse-local -headless"
