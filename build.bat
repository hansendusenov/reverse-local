@echo off
setlocal enabledelayedexpansion

echo ========================================================================
echo   Reverse.Local - Build Script (Windows)
echo ========================================================================

REM Check if Go is installed
where go >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go compiler tidak ditemukan! Pastikan Go 1.22+ sudah terinstall dan ada di PATH.
    pause
    exit /b 1
)

echo Pilih mode build:
echo [1] Release (Silent Tray / Tanpa jendela console CMD hitam) - Direkomendasikan
echo [2] Debug / Console (Menampilkan jendela console CMD untuk melihat log)
echo [3] Jalankan Unit Test terlebih dahulu lalu Build Release
set /p choice="Masukkan pilihan (1/2/3) [default: 1]: "

if "%choice%"=="" set choice=1

if "%choice%"=="3" (
    echo [INFO] Menjalankan test suite...
    go test -v ./...
    if %ERRORLEVEL% neq 0 (
        echo [ERROR] Unit test gagal! Proses build dibatalkan.
        pause
        exit /b 1
    )
    echo [INFO] Semua unit test berhasil.
    set choice=1
)

if "%choice%"=="1" (
    echo [INFO] Membangun binary Release (Silent GUI / Tray background)...
    go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
    if %ERRORLEVEL% equ 0 (
        echo [SUCCESS] Binary berhasil dibuat: reverse-local.exe
        echo.
        echo Petunjuk menjalankan:
        echo Klik kanan pada reverse-local.exe -^> "Run as Administrator"
        echo Atau lewat PowerShell: Start-Process .\reverse-local.exe -Verb runAs
    ) else (
        echo [ERROR] Build gagal!
    )
) else if "%choice%"=="2" (
    echo [INFO] Membangun binary Debug (dengan jendela console CMD)...
    go build -o reverse-local-debug.exe .
    if %ERRORLEVEL% equ 0 (
        echo [SUCCESS] Binary berhasil dibuat: reverse-local-debug.exe
        echo.
        echo Petunjuk menjalankan:
        echo Jalankan lewat CMD/PowerShell sebagai Administrator untuk melihat log real-time.
    ) else (
        echo [ERROR] Build gagal!
    )
) else (
    echo [ERROR] Pilihan tidak valid.
)

pause
