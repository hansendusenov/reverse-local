# Panduan Kompilasi & Build (Build Guide) — Reverse.Local

Dokumen ini berisi panduan lengkap langkah demi langkah untuk melakukan kompilasi (*compile*), *build*, dan menjalankan **Reverse.Local** pada sistem operasi Windows, macOS, dan Linux.

---

## 1. Prasyarat Sistem (Prerequisites)

Sebelum melakukan build, pastikan sistem Anda telah memiliki:
1. **Go (Golang)**: Versi **1.22** atau yang lebih baru.
   - Cek versi Go di terminal:
     ```bash
     go version
     ```
   - Unduh jika belum terpasang: [golang.org/dl](https://go.dev/dl/)
2. **CGO / GCC**:
   - **Windows**: **TIDAK MEMERLUKAN GCC / CGO**. Library system tray yang digunakan (`github.com/getlantern/systray`) pada Windows telah diimplementasikan dengan pure Go dan Windows Win32 API (`golang.org/x/sys/windows`), sehingga build 100% portabel.
   - **Linux**: Jika ingin menjalankan GUI System Tray, butuh paket pustaka `libayatana-appindicator3-dev` atau `libappindicator3-dev`. Namun jika menggunakan mode `-headless` (sangat direkomendasikan untuk server/docker/wsl), **tidak butuh dependensi eksternal apapun**.

---

## 2. Cara Compile & Build di Windows

Terdapat dua mode build yang dapat Anda pilih sesuai kebutuhan:

### A. Mode Production (Rekomendasi: Silent System Tray / Tanpa Jendela Hitam CMD)
Jika Anda ingin aplikasi berjalan murni di latar belakang (*background*) hanya berupa ikon di taskbar / system tray tanpa memunculkan jendela console hitam:

```powershell
go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
```

> **Penjelasan Parameter:**
> - `-H windowsgui`: Menginstruksikan linker Go untuk mengompilasi program sebagai aplikasi GUI Windows (bukan Console), sehingga jendela CMD tidak akan muncul saat dijalankan.
> - `-s -w`: Menghapus tabel simbol debug (*symbol table* dan *DWARF info*), memangkas ukuran file binary hingga ~35% lebih kecil dan mempercepat waktu *startup*.

---

### B. Mode Debug / Development (Dengan Jendela Console untuk Melihat Log)
Jika Anda sedang melakukan pengujian (*debugging*) dan ingin melihat log server, status koneksi proxy, dan aktivitas HTTP/HTTPS secara *real-time*:

```powershell
go build -o reverse-local-debug.exe .
```

---

### C. Build Otomatis Menggunakan Script `build.bat`
Tersedia script otomatis di dalam repositori:
1. Buka folder proyek.
2. Klik ganda file `build.bat`.
3. Pilih opsi **[1] Release** atau **[2] Debug**. Binary akan langsung ter-generate otomatis.

---

## 3. Cara Compile di macOS

```bash
# Build binary standar
go build -ldflags="-s -w" -o reverse-local .

# Jika menggunakan Apple Silicon (M1/M2/M3/M4) atau Intel, Go otomatis mendeteksi arsitektur lokal.
```

---

## 4. Cara Compile di Linux

```bash
# Build binary standar (mode headless / daemon)
go build -ldflags="-s -w" -o reverse-local .
```

---

## 5. Cross-Compilation (Build untuk OS Lain)

Go memungkinkan Anda melakukan *cross-compile* dari satu OS ke OS lainnya tanpa perlu berpindah komputer:

### Membangun file `.exe` Windows dari Linux atau macOS:
```bash
GOOS=windows GOARCH=amd64 go build -ldflags="-H windowsgui -s -w" -o reverse-local.exe .
```

### Membangun binary Linux dari Windows (PowerShell):
```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o reverse-local .
```

### Membangun binary macOS ARM64 (Apple Silicon) dari OS apapun:
```bash
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o reverse-local-darwin-arm64 .
```

---

## 6. Mengapa Reverse.Local Membutuhkan Hak Administrator / Root?

Saat dijalankan, aplikasi memerlukan hak akses Administrator (Windows) atau Root (`sudo` di Linux/macOS) karena 3 alasan teknis:
1. **Mengikat Port Standar 80 (HTTP) dan 443 (HTTPS)**:
   - Port di bawah 1024 merupakan *privileged ports* pada sistem operasi yang hanya boleh dibuka oleh proses yang memiliki hak akses administratif.
   - Dengan port 80 & 443, Anda bisa mengakses `https://myapp.local` langsung tanpa mengetikkan nomor port seperti `:8443` di browser.
2. **Sinkronisasi File `hosts` Sistem Operasi**:
   - Menulis ke `C:\Windows\System32\drivers\etc\hosts` (Windows) atau `/etc/hosts` (macOS/Linux) untuk memetakan domain lokal Anda ke `127.0.0.1`.
3. **Pemasangan Root CA Lokal ke System Trust Store**:
   - Memasukkan sertifikat CA ke Windows Certificate Store (`certutil -addstore -f "Root"`) atau macOS System Keychain agar browser tidak memunculkan peringatan *"Your connection is not private"* (SSL Warning).

---

## 7. Cara Menjalankan dengan Hak Akses Administrator

### A. Di Windows

#### Cara 1: Melalui Klik Kanan (Paling Mudah)
1. Buka File Explorer dan temukan `reverse-local.exe`.
2. Klik kanan pada file tersebut.
3. Pilih **"Run as administrator"** (Jalankan sebagai administrator).
4. Klik **Yes** pada konfirmasi UAC Windows.
5. Ikon Reverse.Local akan muncul di pojok kanan bawah Taskbar / System Tray Anda.

#### Cara 2: Melalui PowerShell Terminal
Buka PowerShell dan jalankan perintah:
```powershell
Start-Process .\reverse-local.exe -Verb runAs
```

#### Cara 3: Membuat Shortcut yang Selalu Otomatis "Run as Administrator"
Jika Anda sering menggunakannya setiap hari:
1. Klik kanan pada `reverse-local.exe` ➔ pilih **Show more options** ➔ **Create shortcut** (Buat pintasan).
2. Pindahkan pintasan tersebut ke Desktop Anda.
3. Klik kanan pada shortcut tersebut ➔ pilih **Properties**.
4. Di tab **Shortcut**, klik tombol **Advanced...** (Lanjutan).
5. Centang kotak **"Run as administrator"** ➔ Klik **OK** ➔ Klik **Apply**.
6. Sekarang setiap kali shortcut diklik, Windows akan otomatis menjalankannya sebagai Administrator.

#### Cara 4: Menjalankan Otomatis saat Windows Menyala (Auto-start on Startup)
1. Tekan tombol `Win + R`, ketik `shell:startup`, lalu tekan Enter.
2. Salin (*copy-paste*) shortcut yang telah dibuat pada Cara 3 ke dalam folder Startup tersebut.

---

### B. Di macOS

Jalankan perintah dengan `sudo` di Terminal:
```bash
sudo ./reverse-local
```
*Ikon Reverse.Local akan muncul di Menu Bar bagian atas.*

---

### C. Di Linux

Jalankan mode headless di latar belakang (*background*):
```bash
sudo ./reverse-local -headless &
```

#### Atau pasang sebagai Systemd Service (Opsional):
Buat file service di `/etc/systemd/system/reverse-local.service`:
```ini
[Unit]
Description=Reverse Local Reverse Proxy
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/reverse-local -headless
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```
Lalu aktifkan:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now reverse-local
```

---

## 8. Verifikasi & Pengujian Hasil Build

Untuk memastikan semua modul berjalan 100% sempurna sebelum digunakan:

```powershell
go test -v ./...
```

Output yang diharapkan:
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
ok      reverse-local/internal/assets   0.829s
ok      reverse-local/internal/certs    1.606s
ok      reverse-local/internal/config   0.922s
ok      reverse-local/internal/hosts    0.648s
ok      reverse-local/internal/proxy    3.275s
```

---

## 9. Pemecahan Masalah (Troubleshooting)

### Port 80 atau 443 sudah digunakan (*Port already in use*)
Jika Anda menerima pesan error bahwa port 80 atau 443 telah dipakai oleh aplikasi lain (misal: IIS, Skype, Apache XAMPP, atau Nginx lokal):
- **Cek proses yang menggunakan port di Windows**:
  ```powershell
  netstat -ano | findstr :80
  netstat -ano | findstr :443
  ```
- **Solusi Alternatif**: Anda dapat menjalankan Reverse.Local pada port kustom dengan flag:
  ```powershell
  .\reverse-local.exe -http 8080 -https 8443
  ```
  *(Dashboard dapat diakses di `https://reverse.local:8443`)*.

### Peringatan Sertifikat di Browser Mozilla Firefox
Windows Trust Store berlaku otomatis untuk Google Chrome, Microsoft Edge, Brave, dan Safari. Firefox memiliki sertifikat internal sendiri:
1. Buka Dashboard di `https://reverse.local`.
2. Klik tombol **"Download Root CA"** di header atas untuk mengunduh `reverse-local-ca.crt`.
3. Di Firefox, buka `Settings` ➔ ketik `Certificates` di kolom pencarian ➔ klik **View Certificates...** ➔ tab **Authorities** ➔ klik **Import...**.
4. Pilih file `reverse-local-ca.crt` yang diunduh, centang *"Trust this CA to identify websites"*, lalu klik **OK**.
