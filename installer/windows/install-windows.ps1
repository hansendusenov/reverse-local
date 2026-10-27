#Requires -Version 5.1
$ErrorActionPreference = "Stop"

# 1. Auto-Elevate to Administrator if not already elevated
$currentPrincipal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "[INFO] Requesting Administrator privileges via Windows UAC..." -ForegroundColor Yellow
    $argList = "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
    Start-Process -FilePath "powershell.exe" -ArgumentList $argList -Verb RunAs
    exit 0
}

Write-Host "========================================================================" -ForegroundColor Red
Write-Host "  Reverse.Local // Windows Administrator Installer" -ForegroundColor White
Write-Host "  Local Reverse Proxy, Native SSH Tunnel (-L -N) & LAN DNS Broadcaster" -ForegroundColor Gray
Write-Host "========================================================================" -ForegroundColor Red

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Definition
$RepoRoot = Resolve-Path (Join-Path $ScriptDir "..\..") -ErrorAction SilentlyContinue
$SourceExe = Join-Path $ScriptDir "reverse-local.exe"
if (-not (Test-Path $SourceExe) -and $RepoRoot) {
    $SourceExe = Join-Path $RepoRoot "reverse-local.exe"
}

if (-not (Test-Path $SourceExe)) {
    Write-Host "[ERROR] reverse-local.exe not found at $SourceExe. Please build it first (go build -o reverse-local.exe .)." -ForegroundColor Red
    Read-Host "Press Enter to exit"
    exit 1
}

$InstallDir = Join-Path $env:ProgramFiles "ReverseLocal"
$TargetExe = Join-Path $InstallDir "reverse-local.exe"

# 2. Stop existing running process if active
Write-Host "[1/5] Stopping any running Reverse.Local instance..." -ForegroundColor Cyan
Get-Process -Name "reverse-local" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 400

# 3. Copy executable to C:\Program Files\ReverseLocal
Write-Host "[2/5] Installing binary to $InstallDir..." -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Copy-Item -Path $SourceExe -Destination $TargetExe -Force

# 4. Configure Windows Firewall rules for HTTP/HTTPS Proxy (80, 443) and LAN DNS/mDNS (53, 5353)
Write-Host "[3/5] Configuring Windows Defender Firewall rules for Proxy & LAN DNS Broadcast..." -ForegroundColor Cyan
netsh advfirewall firewall delete rule name="Reverse.Local HTTP/HTTPS Proxy" | Out-Null
netsh advfirewall firewall add rule name="Reverse.Local HTTP/HTTPS Proxy" dir=in action=allow program="$TargetExe" protocol=TCP localport=80,443 profile=private,domain,public | Out-Null

netsh advfirewall firewall delete rule name="Reverse.Local LAN DNS & mDNS" | Out-Null
netsh advfirewall firewall add rule name="Reverse.Local LAN DNS & mDNS" dir=in action=allow program="$TargetExe" protocol=UDP localport=53,5353 profile=private,domain | Out-Null

# 5. Create Desktop & Start Menu Shortcuts
Write-Host "[4/5] Creating Desktop and Start Menu shortcuts..." -ForegroundColor Cyan
$WshShell = New-Object -ComObject WScript.Shell

$DesktopPath = [Environment]::GetFolderPath("Desktop")
$DesktopShortcut = $WshShell.CreateShortcut((Join-Path $DesktopPath "Reverse.Local.lnk"))
$DesktopShortcut.TargetPath = $TargetExe
$DesktopShortcut.Arguments = "-control"
$DesktopShortcut.WorkingDirectory = $InstallDir
$DesktopShortcut.Description = "Reverse.Local - Reverse Proxy, SSH Tunnel & LAN DNS Manager"
$DesktopShortcut.Save()

$StartMenuPath = [Environment]::GetFolderPath("CommonPrograms")
$StartShortcut = $WshShell.CreateShortcut((Join-Path $StartMenuPath "Reverse.Local.lnk"))
$StartShortcut.TargetPath = $TargetExe
$StartShortcut.Arguments = "-control"
$StartShortcut.WorkingDirectory = $InstallDir
$StartShortcut.Description = "Reverse.Local - Reverse Proxy, SSH Tunnel & LAN DNS Manager"
$StartShortcut.Save()

# 6. Launch Reverse.Local Control Panel
Write-Host "[5/5] Launching Reverse.Local..." -ForegroundColor Green
Start-Process -FilePath $TargetExe -ArgumentList "-control" -WorkingDirectory $InstallDir

Write-Host ""
Write-Host "[OK] Installation complete!" -ForegroundColor Green
Write-Host "  Binary   : $TargetExe"
Write-Host "  Configs  : $env:APPDATA\reverse-local\ (routes.json, tunnels.json, settings.json)"
Write-Host "  Workbench: https://reverse.local"
Start-Sleep -Seconds 3

