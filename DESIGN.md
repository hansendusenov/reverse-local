# Design Direction: Reverse.Local

## Design Read & Dials
Reading this as: **Swiss Precision network routing, SSH port forwarding & TLS inspection workbench** for **software engineers running local development servers**, in a **Merah Putih (Crimson Red & Crisp Alabaster White) instrument** style, dial **ENERGY 2 / RHYTHM 3 / MOTION 1**.

- **Dial:** `ENERGY 2 / RHYTHM 3 / MOTION 1`
- **Primary Screen Job:** Provide a side-by-side dual-pane workbench where engineers can bind `.local`/`.test`/`.dev` domains and `-L -N` SSH tunnels on the left while monitoring live HTTP/WebSocket traffic and TLS status on the right, paired with a fixed-size `480x660px` Desktop Control Panel (`GET /control`) for daemon settings.

## Identity & Motifs
- **Identity Motif:** Crisp 1px structural borders (`#e4e4e7`), bold Crimson Red top accent bar (`#dc2626`), indexed module headers (`[01] ROUTING MATRIX`, `[01B] SSH PORT FORWARDING`, `[02] LIVE TRAFFIC STREAM`), inline quick-bind composer bar, and monospaced routing pipeline strips (`https://domain.local -> http://127.0.0.1:port`).
- **Focal Point:** The **Inline Quick-Bind Composer** with its **Crimson Red (`#dc2626`)** primary action button at the top of the left workbench pane.

## Color & Contrast Tokens (WCAG 2.2 AA Verified via `contrast-check.py`)
- **Canvas & Surfaces (60% White / Neutral, 30% Structural Ink):**
  - `surface.base`: `#fafafa` (warm alabaster white canvas).
  - `surface.panel`: `#ffffff` (pure crisp white workbench pane surface).
  - `surface.elevated`: `#f4f4f5` (subtle zinc-100 module headers, active tabs, and hover rows).
  - `surface.seam`: `#e4e4e7` (1px structural grid hairlines and table dividers).
  - `surface.controlBorder`: `#71717a` (interactive input, button, and toggle boundaries; verified **4.83:1** on `#ffffff`, passing WCAG 1.4.11 3:1 non-text contrast).
- **Text Ramp:**
  - Primary Ink (`#18181b` Zinc-900): Verified **17.72:1** on `#ffffff` and **16.97:1** on `#fafafa` (PASS >= 4.5:1).
  - Secondary / Muted Text (`#52525b` Zinc-600): Verified **7.73:1** on `#ffffff` and **7.03:1** on `#f4f4f5` (PASS >= 4.5:1).
- **Deliberate Crimson Red Accent (10% Merah Putih):**
  - `brand.red`: `#dc2626` (Crimson Red-600 primary CTA background with `#ffffff` white text at **4.83:1** PASS >= 4.5:1, active filter tabs, and keyboard focus rings).
  - `brand.redHover`: `#b91c1c` (Crimson Red-700 hover state with `#ffffff` white text at **6.47:1** PASS >= 4.5:1).
  - `brand.redTint`: `#fef2f2` surface with `#b91c1c` text (verified **5.91:1** PASS >= 4.5:1).
- **Semantic Feedback Colors (Light-Surface Calibrated for WCAG AA):**
  - Live / Active / `2xx` (`#047857` Emerald-700): Verified **5.48:1** on `#ffffff` and **5.21:1** on `#ecfdf5`.
  - Upstream Target / Info / `3xx` (`#0369a1` Sky-700): Verified **5.93:1** on `#ffffff`.
  - Warning / `4xx` / Paused (`#b45309` Amber-700): Verified **5.02:1** on `#ffffff`.
  - Danger / `5xx` / Error (`#b91c1c` Crimson-700): Verified **6.47:1** on `#ffffff` and **5.91:1** on `#fef2f2`.

## Typography & Numeric Discipline
- **UI Sans (`IBM Plex Sans`):** Chosen over default `Inter` for its technical, Swiss-instrument character and crisp legibility in high-contrast white control panels.
- **Technical Mono (`IBM Plex Mono`):** Chosen over default `JetBrains Mono` for uniform engineering readouts across domains, URLs, ports, IP addresses, HTTP methods, status codes, cURL snippets, and timestamps.
- **Tabular Numerals (`tabular-nums`):** Applied to all counters, ports, status codes, byte counts, timestamps, and latency durations (`ms`).

## One-Line Decision Log (R-31)
- **Asymmetric Split-Pane Workbench Layout (R-05 / RHYTHM 3):** Places the Routing Matrix, Inline Composer & Native Go SSH Port Forwarding Matrix (`5 cols`) beside the Live Traffic Stream (`7 cols`) on desktop so engineers can trigger requests and watch traffic side-by-side.
- **Locked Fixed-Size Desktop Control Panel (`web/control.html` + `winlock_windows.go`):** Separates daemon-level settings (`settings.json`, port bindings, LAN/Localhost mode, Root CA trust, DNS flush) into a non-resizable, non-maximizable `480x660px` desktop utility window (`WS_THICKFRAME` & `WS_MAXIMIZEBOX` stripped via Win32 API).
- **Swiss Merah Putih Palette (R-01 / R-21):** Replaces dark carbon/amber with a clean high-contrast Crisp White (`#ffffff`/`#fafafa`) & Crimson Red (`#dc2626`/`#b91c1c`) palette following the 60-30-10 rule.
- **Responsive Reflow (R-03):** Collapses the 12-column split workbench into a clean stacked/tabbed mobile layout below `1024px` with `16px` (`text-base`) inputs and `>=44px` touch targets.
