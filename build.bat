@echo off
setlocal enabledelayedexpansion

echo Reverse.Local Build Script (Windows)
echo.

where go >nul 2>nul
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go compiler not found. Install Go 1.22+ and ensure it is in your PATH.
    pause
    exit /b 1
)

echo Select build target:
echo [1] Release (Silent System Tray without console window) - Default
echo [2] Debug / Console (With console window for real-time logs)
echo [3] Run unit test suite first, then build Release
set /p choice="Enter choice (1/2/3) [default: 1]: "

if "%choice%"=="" set choice=1

if "%choice%"=="3" (
    echo [INFO] Running unit tests...
    go test -v ./...
    if %ERRORLEVEL% neq 0 (
        echo [ERROR] Unit tests failed. Build aborted.
        pause
        exit /b 1
    )
    echo [INFO] All unit tests passed.
    set choice=1
)

if "%choice%"=="1" (
    echo [INFO] Building Release binary (silent system tray)...
    go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
    if %ERRORLEVEL% equ 0 (
        echo [SUCCESS] Built binary: reverse-local.exe
        echo.
        echo To run with Administrator privileges:
        echo   Right-click reverse-local.exe -^> "Run as administrator"
        echo   Or in PowerShell: Start-Process .\reverse-local.exe -Verb runAs
    ) else (
        echo [ERROR] Release build failed.
    )
) else if "%choice%"=="2" (
    echo [INFO] Building Debug binary (with console output)...
    go build -o reverse-local-debug.exe .
    if %ERRORLEVEL% equ 0 (
        echo [SUCCESS] Built binary: reverse-local-debug.exe
        echo.
        echo To run:
        echo   Launch from an elevated PowerShell or Command Prompt to inspect live logs.
    ) else (
        echo [ERROR] Debug build failed.
    )
) else (
    echo [ERROR] Invalid selection.
)

pause
