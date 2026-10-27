# Reverse.Local

Portable, cross-platform local reverse proxy, automatic `hosts` file manager, local SSL/TLS certificate generator, and native Go SSH port forwarder (`-L -N`), controlled via a System Tray icon, a compact Desktop Control Panel, and a self-hosted Browser Workbench.

**Engineered by Hansen Dusenov** • Copyright &copy; 2026 Hansen Dusenov. All rights reserved.

> **Complete Compilation & Build Guide**: See [BUILD.md](file:///d:/Code/reverse_local/BUILD.md) for instructions on building the silent system tray executable, running the automated `build.bat` and `build.sh` scripts, cross-compiling, and configuring Administrator/root privileges.

---

## Features

- **System Tray & Frameless Desktop Control Panel (`GET /control`)**:
  - **Open Control Panel (Settings)**: Launches a compact 480x640px desktop utility window (`--app` mode) for daemon settings (`settings.json`), HTTP/HTTPS port configuration, `127.0.0.1` vs `0.0.0.0` LAN binding, OS Startup registration, Root CA installation, and 1-click OS DNS cache flushing.
  - **Open Browser Workbench**: Opens `https://reverse.local` in your default web browser for full-screen domain routing, SSH tunnel management, and live traffic inspection.
  - **Sync Hosts & Flush DNS**: Synchronizes active domains with the OS `hosts` file and flushes the OS DNS resolver cache.
  - **Quit**: Automatically strips managed entries from your OS `hosts` file, closes active SSH tunnels, and exits cleanly.
- **Native Go SSH Port Forwarding (`-L -N`)**:
  - Built-in pure-Go SSH client (`golang.org/x/crypto/ssh`) for local port forwarding (`127.0.0.1:<local_port> -> <remote_host>:<remote_port>`) with zero external `ssh.exe` child processes.
  - Supports **Private Key** (`~/.ssh/id_ed25519`, `~/.ssh/id_rsa`, custom PEM path with optional passphrase), **Password**, and **SSH Agent** authentication.
  - Real-time connection status, active stream counters, RX/TX byte telemetry, SSH keepalive pings, and automatic reconnection with exponential backoff.
  - **1-Click Bind to Domain**: Map any forwarded SSH port directly to an HTTPS `.local` domain in the Workbench.
- **Self-Hosted Browser Workbench (`https://reverse.local`)**:
  - Embedded directly into the single Go binary (`//go:embed`). No external assets or `node_modules` needed.
  - Industrial Split-Pane Workbench (`DESIGN.md`) with inline Quick-Bind composer, active/paused toggles, copy helpers, and live SSE traffic stream.
  - Direct REST API for programmatic automation.
- **Dynamic On-The-Fly SSL/TLS Certificates**:
  - Generates a local Root Certificate Authority (CA) upon first launch.
  - Auto-installs into the OS Trust Store (`certutil` on Windows, `security` on macOS, `update-ca-certificates` on Linux).
  - Dynamically issues SNI leaf TLS certificates on-the-fly (`tls.Config.GetCertificate`) for `reverse.local` and any user-configured domain.
  - Automatic HTTP (Port 80) to HTTPS (Port 443) redirection.
- **Zero-Corruption OS `hosts` Synchronization**:
  - Managed block safely isolated between explicit markers:
    ```text
    # BEGIN LOCAL-REVERSE-PROXY
    127.0.0.1 reverse.local
    127.0.0.1 myapp.local
    # END LOCAL-REVERSE-PROXY
    ```
  - Automatically synced upon route creation, update, toggle, or deletion.
  - Completely removed upon application exit.
- **Live Reverse Proxy Access Logs & Telemetry**:
  - High-performance, thread-safe in-memory circular ring buffer (1,000 entries) capturing all incoming domain traffic.
  - Intercepts and records: timestamp, client IP, HTTP method, host, path, target upstream backend URL, status codes, roundtrip latency (ms), payload bytes transferred, protocol (`HTTP/1.1`, `HTTP/2.0`, `WebSocket`), user-agent, and error diagnosis (e.g. connection refused).
  - Real-time streaming to the dashboard via Server-Sent Events (SSE: `/api/logs/stream`) with pause/resume and zero polling overhead.
  - Filter by HTTP status classes (`2xx`, `3xx`, `4xx`, `5xx`) and instant multi-field search.
  - Interactive Request Inspector modal with 1-click **Copy as cURL** command for rapid API testing and reproduction.
  - One-click JSON export for offline diagnostics and sharing.
  - Formatted terminal access logging in console/debug mode (`[ACCESS] 200 GET https://api.local/v1/users -> http://localhost:8080 (4ms)`).
- **WebSocket & Header Forwarding**:
  - Full bidirectional WebSocket proxying (`Upgrade: websocket`).
  - Standard headers injected: `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Forwarded-For`, and `X-Real-IP`.
  - Rewrite of upstream `Host` to match backend target, avoiding "Invalid Host Header" issues in frameworks like Vite, Next.js, and Webpack.

---

## Project Structure

```text
reverse-local/
├── go.mod
├── main.go                 # Entrypoint, Admin/Root check, Systray init, Server lifecycle
├── build.bat               # Windows 1-click build script (Release & Debug)
├── build.sh                # Linux/macOS build script
├── internal/
│   ├── admin/
│   │   ├── check_windows.go # Windows Token.IsElevated() check
│   │   └── check_unix.go    # Unix/macOS root (UID 0) check
│   ├── assets/
│   │   ├── icon.go          # Runtime generator for 32x32 PNG and Windows ICO tray icons
│   │   └── icon_test.go
│   ├── certs/
│   │   ├── ca.go            # Local Root CA & dynamic SNI TLS certificate generator
│   │   └── ca_test.go
│   ├── config/
│   │   ├── store.go         # Thread-safe JSON persistence (sync.RWMutex) for CRUD routes
│   │   └── store_test.go
│   ├── hosts/
│   │   ├── manager.go       # Cross-platform /etc/hosts & Windows hosts block sync
│   │   └── manager_test.go
│   ├── logger/
│   │   ├── logger.go        # Thread-safe ring buffer, SSE broadcasting & filtering
│   │   └── logger_test.go
│   ├── proxy/
│   │   ├── server.go        # HTTP/HTTPS servers, Dynamic Reverse Proxy, and REST API router
│   │   └── server_test.go
│   ├── settings/
│   │   ├── settings.go      # Thread-safe daemon settings (settings.json) & OS utilities
│   │   └── settings_test.go
│   └── tunnel/
│       ├── manager.go       # Native Go SSH local port forwarding (-L -N) engine
│       └── manager_test.go
└── web/
    ├── web.go              # go:embed wrapper for embedded assets
    ├── control.html        # Embedded 480x640 Desktop Control Panel UI
    └── index.html          # Embedded Browser Workbench UI (HTML + Vanilla JS + Tailwind CSS)
```

---

## How to Compile & Run with Admin/Root Privileges

Modifying the system `hosts` file and binding to privileged standard ports (:80 and :443) require elevated permissions.

### Windows

1. **Compile**:
   ```powershell
   go build -o reverse-local.exe .
   ```
2. **Run as Administrator**:
   - Right-click `reverse-local.exe` and select **"Run as Administrator"**, or from an elevated PowerShell terminal:
     ```powershell
     Start-Process .\reverse-local.exe -Verb runAs
     ```
   - Alternatively, to run in headless console mode without system tray:
     ```powershell
     .\reverse-local.exe -headless
     ```

### macOS

1. **Compile**:
   ```bash
   go build -o reverse-local .
   ```
2. **Run with `sudo`**:
   ```bash
   sudo ./reverse-local
   ```
   *Note: On macOS, GUI apps running under sudo will display the tray icon in the top macOS menu bar.*

### Linux

1. **Compile**:
   ```bash
   go build -o reverse-local .
   ```
2. **Run with `sudo`**:
   ```bash
   sudo ./reverse-local -headless
   ```
   *(Use `-headless` on servers or headless displays; for desktop environments with tray support, omit `-headless`)*.

---

## Command Line Flags

| Flag | Default | Description |
|---|---|---|
| `-headless` | `false` | Run in daemon/console mode without system tray |
| `-control` | `false` | Open the compact Desktop Control Panel window on startup |
| `-http` | `80` (or `settings.json`) | HTTP listening port (redirects to HTTPS) |
| `-https` | `443` (or `settings.json`) | HTTPS listening port (reverse proxy & dashboard) |
| `-config` | OS User Config Dir | Path to `routes.json` |
| `-hosts` | OS Default (`/etc/hosts` or System32) | Path to OS hosts file |
| `-certs` | OS User Config Dir | Directory storing Root CA (`ca.crt` & `ca.key`) |

---

## REST API Specification

The management API is served directly under `https://reverse.local/api/...` (or `https://127.0.0.1/api/...`):

### 1. List Routes
- **Endpoint**: `GET /api/routes`
- **Response**: `200 OK`
```json
[
  {
    "id": "e0b52a1c43f8",
    "domain": "myapp.local",
    "target_url": "http://127.0.0.1:3000",
    "active": true,
    "created_at": "2026-10-27T12:00:00Z"
  }
]
```

### 2. Create Route
- **Endpoint**: `POST /api/routes`
- **Body**:
```json
{
  "domain": "api.local",
  "target_url": "http://127.0.0.1:8080"
}
```
- **Response**: `201 Created`

### 3. Update Route
- **Endpoint**: `PUT /api/routes/{id}`
- **Body**:
```json
{
  "domain": "api.local",
  "target_url": "http://127.0.0.1:8081",
  "active": true
}
```
- **Response**: `200 OK`

### 4. Toggle Route Active State
- **Endpoint**: `POST /api/routes/{id}/toggle`
- **Response**: `200 OK`

### 5. Delete Route
- **Endpoint**: `DELETE /api/routes/{id}`
- **Response**: `200 OK`

### 6. System Status
- **Endpoint**: `GET /api/status`
- **Response**: `200 OK`
```json
{
  "status": "running",
  "http_port": 80,
  "https_port": 443,
  "ca_installed": true,
  "active_routes": 3,
  "total_routes": 4,
  "os": "windows"
}
```

### 7. Download Root CA Certificate
- **Endpoint**: `GET /ca.crt`
- **Response**: `200 OK` (PEM file with `Content-Type: application/x-x509-ca-cert`)

### 8. Install Root CA to OS Trust Store
- **Endpoint**: `POST /api/ca/install`
- **Response**: `200 OK`

### 9. Query Access Logs
- **Endpoint**: `GET /api/logs?limit=100&search=api`
- **Response**: `200 OK`
```json
[
  {
    "id": "e9b276ab012f",
    "timestamp": "2026-10-27T12:00:00Z",
    "client_ip": "127.0.0.1",
    "method": "GET",
    "host": "api.local",
    "path": "/v1/users",
    "target_url": "http://127.0.0.1:8080",
    "status_code": 200,
    "duration_ms": 3,
    "bytes_sent": 1420,
    "proto": "HTTP/2.0",
    "user_agent": "Mozilla/5.0 ...",
    "error": ""
  }
]
```

### 10. Clear Access Logs
- **Endpoint**: `DELETE /api/logs`
- **Response**: `200 OK`

### 11. Real-Time Access Logs Streaming (SSE)
- **Endpoint**: `GET /api/logs/stream`
- **Headers**: `Content-Type: text/event-stream`
- **Payload**: Server-Sent Event stream emitting JSON-encoded `LogEntry` objects on every intercepted request.

---

## Running the Tests

To run the complete unit test suite across all modules:
```bash
go test -v ./...
```
