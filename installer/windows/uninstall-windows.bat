@echo off
title Reverse.Local - Windows Uninstaller
net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [INFO] Requesting Administrator privileges to uninstall Reverse.Local...
    powershell -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
    exit /b
)

echo [1/4] Stopping Reverse.Local daemon...
taskkill /F /IM reverse-local.exe >nul 2>&1

echo [2/4] Removing Windows Firewall rules and startup registry key...
netsh advfirewall firewall delete rule name="Reverse.Local HTTP/HTTPS Proxy" >nul 2>&1
netsh advfirewall firewall delete rule name="Reverse.Local LAN DNS & mDNS" >nul 2>&1
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v "ReverseLocal" /f >nul 2>&1

echo [3/4] Removing Desktop and Start Menu shortcuts...
del /F /Q "%USERPROFILE%\Desktop\Reverse.Local.lnk" >nul 2>&1
del /F /Q "%ProgramData%\Microsoft\Windows\Start Menu\Programs\Reverse.Local.lnk" >nul 2>&1

echo [4/4] Removing C:\Program Files\ReverseLocal...
rmdir /S /Q "%ProgramFiles%\ReverseLocal" >nul 2>&1

echo.
echo [OK] Reverse.Local has been uninstalled.
echo Saved routes and SSH tunnel configs in "%APPDATA%\reverse-local" were preserved.
pause

