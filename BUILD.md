# Compilation & Build Guide: Reverse.Local

Step-by-step instructions to compile, build, and run **Reverse.Local** on Windows, macOS, and Linux.

---

## 1. Prerequisites

Before building, ensure your environment has:
1. **Go (Golang)**: Version **1.22** or newer.
   - Verify your installed version:
     ```bash
     go version
     ```
   - Download from [go.dev/dl](https://go.dev/dl/) if not installed.
2. **CGO / C Compiler**:
   - **Windows**: **No GCC or CGO required**. The system tray library (`github.com/getlantern/systray`) uses pure Go and native Win32 API calls (`golang.org/x/sys/windows`), producing a 100% portable binary.
   - **Linux**: Running the GUI system tray requires `libayatana-appindicator3-dev` or `libappindicator3-dev`. Running in `-headless` mode (recommended for servers, Docker, and WSL) requires zero external C libraries.

---

## 2. Building on Windows

Choose between two build modes depending on your workflow:

### A. Production Release Mode (Silent System Tray without Console Window)
Compiles Reverse.Local as a background GUI utility that lives in the Windows System Tray without opening a command prompt window:

```powershell
go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
```

**Linker Flags:**
- `-H windowsgui`: Instructs the Go linker to build a Windows GUI subsystem binary instead of a console binary, suppressing the terminal window on launch.
- `-s -w`: Strips the symbol table and DWARF debug metadata, reducing binary size by roughly 35%.

---

### B. Debug / Console Mode (With Terminal Window for Real-Time Logs)
Keeps the console window attached so you can inspect startup diagnostics and live `[ACCESS]` HTTP/HTTPS logs in real time:

```powershell
go build -o reverse-local-debug.exe .
```

---

### C. Automated Build via `build.bat`
You can also run the included batch script:
1. Open the repository root folder.
2. Run `build.bat`.
3. Select **[1] Release**, **[2] Debug**, or **[3] Run Unit Tests then Build Release**.

---

## 3. Building on macOS

```bash
go build -ldflags="-s -w" -o reverse-local .
```
Go automatically targets your native architecture (Apple Silicon `arm64` or Intel `amd64`).

---

## 4. Building on Linux

```bash
go build -ldflags="-s -w" -o reverse-local .
```

---

## 5. Cross-Compilation

Build binaries for other operating systems from any machine:

### Build Windows `.exe` from Linux or macOS
```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
```

### Build Linux Binary from Windows (PowerShell)
```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o reverse-local .
```

### Build macOS ARM64 (Apple Silicon) Binary
```bash
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o reverse-local-darwin-arm64 .
```

---

## 6. Why Administrator / Root Privileges Are Required

Reverse.Local requires Administrator (Windows) or `root` (`sudo` on macOS/Linux) permissions for three operating system operations:
1. **Binding Privileged Ports 80 (HTTP) and 443 (HTTPS)**:
   - Ports below `1024` are restricted to elevated processes on Unix and Windows systems.
   - Binding directly to `:80` and `:443` lets you open `https://myapp.local` in your browser without appending custom port numbers.
2. **Synchronizing the OS `hosts` File**:
   - Writing managed domain entries to `C:\Windows\System32\drivers\etc\hosts` (Windows) or `/etc/hosts` (macOS/Linux) requires administrative write access.
3. **Registering the Local Root CA in the OS Trust Store**:
   - Running `certutil -addstore -f "Root"` (Windows), `security add-trusted-cert` (macOS), or `update-ca-certificates` (Linux) so browsers trust local HTTPS domains without certificate warnings.

---

## 7. Running with Elevated Privileges

### A. Windows

#### Option 1: Context Menu
1. Right-click `reverse-local.exe` in File Explorer.
2. Select **Run as administrator** and confirm the Windows UAC prompt.
3. The Reverse.Local icon will appear in your Windows System Tray.

#### Option 2: PowerShell
```powershell
Start-Process .\reverse-local.exe -Verb runAs
```

#### Option 3: Always Run Shortcut as Administrator
1. Right-click `reverse-local.exe` and select **Create shortcut**.
2. Right-click the new shortcut and open **Properties**.
3. Under the **Shortcut** tab, click **Advanced...**.
4. Check **Run as administrator**, then click **OK** and **Apply**.

#### Option 4: Launch Automatically at Windows Startup
1. Press `Win + R`, type `shell:startup`, and press Enter.
2. Copy the administrator shortcut from Option 3 into the Startup folder.

---

### B. macOS

```bash
sudo ./reverse-local
```
The tray icon appears in the top macOS Menu Bar.

---

### C. Linux

Run in background headless mode:
```bash
sudo ./reverse-local -headless &
```

#### Optional `systemd` Service (`/etc/systemd/system/reverse-local.service`)
```ini
[Unit]
Description=Reverse.Local Reverse Proxy Daemon
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/reverse-local -headless
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

Enable and start the service:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now reverse-local
```

---

## 8. Running the Unit Test Suite

Verify all packages before building:

```powershell
go test -v ./...
```

Expected output:
```text
=== RUN   TestGetAppIcons
--- PASS: TestGetAppIcons (0.00s)
=== RUN   TestCAManager
--- PASS: TestCAManager (0.43s)
=== RUN   TestStoreCRUD
--- PASS: TestStoreCRUD (0.02s)
=== RUN   TestHostsManager
--- PASS: TestHostsManager (0.04s)
=== RUN   TestAPIRoutesCRUD
--- PASS: TestAPIRoutesCRUD (0.17s)
=== RUN   TestReverseProxyForwarding
--- PASS: TestReverseProxyForwarding (0.17s)
PASS
ok      reverse-local/internal/assets
ok      reverse-local/internal/certs
ok      reverse-local/internal/config
ok      reverse-local/internal/hosts
ok      reverse-local/internal/logger
ok      reverse-local/internal/proxy
```

---

## 9. Troubleshooting

### Port 80 or 443 Already in Use
If another process (such as IIS, Apache, Nginx, or Skype) is already listening on port `:80` or `:443`:
- **Inspect bound ports on Windows**:
  ```powershell
  netstat -ano | findstr :80
  netstat -ano | findstr :443
  ```
- **Use custom ports via CLI flags**:
  ```powershell
  .\reverse-local.exe -http 8080 -https 8443
  ```
  *(Access the dashboard at `https://reverse.local:8443`)*.

### Certificate Warning in Mozilla Firefox
Chrome, Edge, Brave, and Safari read the OS Trust Store by default. Firefox uses its own internal certificate store:
1. Open the Reverse.Local dashboard at `https://reverse.local`.
2. Click **Download CA (.crt)** in the top bar to save `reverse-local-ca.crt` (or enable `security.enterprise_roots.enabled` in `about:config`).
3. In Firefox, open **Settings**, search for **Certificates**, click **View Certificates...**, open the **Authorities** tab, and click **Import...**.
4. Select `reverse-local-ca.crt`, check **Trust this CA to identify websites**, and click **OK**.
