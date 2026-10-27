package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidPort        = errors.New("port must be between 1 and 65535")
	ErrDuplicatePorts     = errors.New("HTTP port and HTTPS port cannot be identical")
	ErrInvalidBindAddress = errors.New("bind address must be 127.0.0.1 (localhost) or 0.0.0.0 (LAN)")
	ErrInvalidKeepAlive   = errors.New("SSH keepalive interval must be between 5 and 300 seconds")
)

// Settings holds daemon-level configuration for Reverse.Local.
type Settings struct {
	HTTPPort         int    `json:"http_port"`
	HTTPSPort        int    `json:"https_port"`
	BindAddress      string `json:"bind_address"`
	AutoSyncHosts    bool   `json:"auto_sync_hosts"`
	AutoStartOnBoot  bool   `json:"auto_start_on_boot"`
	SSHKeepAliveSec  int    `json:"ssh_keepalive_sec"`
	SSHAutoReconnect bool   `json:"ssh_auto_reconnect"`
	LANBroadcast     bool   `json:"lan_broadcast"`
	LANDNSPort       int    `json:"lan_dns_port"`
	UpdatedAt        string `json:"updated_at"`
}

// DefaultSettings returns the factory default daemon configuration.
func DefaultSettings() Settings {
	return Settings{
		HTTPPort:         80,
		HTTPSPort:        443,
		BindAddress:      "127.0.0.1",
		AutoSyncHosts:    true,
		AutoStartOnBoot:  false,
		SSHKeepAliveSec:  15,
		SSHAutoReconnect: true,
		LANBroadcast:     true,
		LANDNSPort:       53,
		UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
}

// DefaultSettingsPath returns the default path for settings.json in the user's config directory.
func DefaultSettingsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "settings.json"
	}
	return filepath.Join(configDir, "reverse-local", "settings.json")
}

// Store manages thread-safe persistence for daemon settings.
type Store struct {
	mu       sync.RWMutex
	filePath string
	current  Settings
}

// NewStore loads settings from disk or initializes defaults at filePath.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = DefaultSettingsPath()
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create settings directory %s: %w", dir, err)
	}

	s := &Store{
		filePath: filePath,
		current:  DefaultSettings(),
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) load() error {
	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		return s.saveLocked()
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", s.filePath, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return s.saveLocked()
	}

	loaded := DefaultSettings()
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to parse settings JSON: %w", err)
	}

	if err := Validate(loaded); err != nil {
		loaded = DefaultSettings()
	}

	s.current = loaded
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.current, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", s.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp settings file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to replace settings file: %w", err)
	}

	return nil
}

// Validate checks whether a Settings payload contains valid port, bind, and interval values.
func Validate(cfg Settings) error {
	if cfg.HTTPPort < 1 || cfg.HTTPPort > 65535 {
		return ErrInvalidPort
	}
	if cfg.HTTPSPort < 1 || cfg.HTTPSPort > 65535 {
		return ErrInvalidPort
	}
	if cfg.HTTPPort == cfg.HTTPSPort {
		return ErrDuplicatePorts
	}
	bind := strings.TrimSpace(cfg.BindAddress)
	if bind != "127.0.0.1" && bind != "0.0.0.0" {
		return ErrInvalidBindAddress
	}
	if cfg.SSHKeepAliveSec < 5 || cfg.SSHKeepAliveSec > 300 {
		return ErrInvalidKeepAlive
	}
	return nil
}

// Get returns a snapshot of the current daemon settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Update validates and persists new daemon settings.
func (s *Store) Update(next Settings) (Settings, error) {
	next.BindAddress = strings.TrimSpace(next.BindAddress)
	if next.BindAddress == "" {
		next.BindAddress = "127.0.0.1"
	}
	if next.SSHKeepAliveSec == 0 {
		next.SSHKeepAliveSec = 15
	}
	if next.LANDNSPort <= 0 || next.LANDNSPort > 65535 {
		next.LANDNSPort = 53
	}

	if err := Validate(next); err != nil {
		return Settings{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	prevAutoStart := s.current.AutoStartOnBoot
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.current = next

	if err := s.saveLocked(); err != nil {
		return Settings{}, err
	}

	if prevAutoStart != next.AutoStartOnBoot {
		if err := syncOSAutoStart(next.AutoStartOnBoot); err != nil {
			// Loggable warning by caller; do not fail settings persistence on non-Windows or restricted env
			_ = err
		}
	}

	return s.current, nil
}

// FilePath returns the path to settings.json.
func (s *Store) FilePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filePath
}

// ConfigDir returns the directory containing settings.json.
func (s *Store) ConfigDir() string {
	return filepath.Dir(s.FilePath())
}

// FlushDNSCache flushes the operating system's DNS resolver cache.
func FlushDNSCache() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("ipconfig", "/flushdns")
	case "darwin":
		cmd = exec.Command("dscacheutil", "-flushcache")
	case "linux":
		if _, err := exec.LookPath("resolvectl"); err == nil {
			cmd = exec.Command("resolvectl", "flush-caches")
		} else {
			cmd = exec.Command("systemd-resolve", "--flush-caches")
		}
	default:
		return "", fmt.Errorf("DNS cache flush not supported on %s", runtime.GOOS)
	}

	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if err != nil {
		return msg, fmt.Errorf("failed to flush DNS cache: %w (%s)", err, msg)
	}
	if msg == "" {
		msg = "OS DNS resolver cache flushed."
	}
	return msg, nil
}

// OpenPathInFileManager opens a directory or file in the native OS file manager.
func OpenPathInFileManager(targetPath string) error {
	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("target path does not exist: %w", err)
	}
	dirPath := targetPath
	if !info.IsDir() {
		dirPath = filepath.Dir(targetPath)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dirPath)
	case "darwin":
		cmd = exec.Command("open", dirPath)
	default:
		cmd = exec.Command("xdg-open", dirPath)
	}
	return cmd.Start()
}

// LaunchBrowser opens targetURL in the user's default web browser.
func LaunchBrowser(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	case "darwin":
		cmd = exec.Command("open", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

// LaunchControlPanelApp opens the Desktop Control Panel in a fixed 480x660 frameless native app window
// using Edge or Chrome --app mode and locks its Win32 window frame against resizing or maximizing.
func LaunchControlPanelApp(controlURL string) error {
	profileDir := filepath.Join(filepath.Dir(DefaultSettingsPath()), "control-app-profile")
	_ = os.MkdirAll(profileDir, 0755)

	appArgs := []string{
		"--app=" + controlURL,
		"--window-size=480,660",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
	}

	switch runtime.GOOS {
	case "windows":
		candidates := []string{
			"msedge",
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			"chrome",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		}
		for _, bin := range candidates {
			if path, err := exec.LookPath(bin); err == nil {
				cmd := exec.Command(path, appArgs...)
				if err := cmd.Start(); err == nil {
					EnforceFixedControlPanelWindow()
					return nil
				}
			} else if _, statErr := os.Stat(bin); statErr == nil {
				cmd := exec.Command(bin, appArgs...)
				if err := cmd.Start(); err == nil {
					EnforceFixedControlPanelWindow()
					return nil
				}
			}
		}
	case "darwin":
		macCandidates := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		}
		for _, bin := range macCandidates {
			if _, err := os.Stat(bin); err == nil {
				cmd := exec.Command(bin, appArgs...)
				if err := cmd.Start(); err == nil {
					return nil
				}
			}
		}
	default:
		linuxCandidates := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
		for _, bin := range linuxCandidates {
			if path, err := exec.LookPath(bin); err == nil {
				cmd := exec.Command(path, appArgs...)
				if err := cmd.Start(); err == nil {
					return nil
				}
			}
		}
	}

	return LaunchBrowser(controlURL)
}

func syncOSAutoStart(enable bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(exePath), ".test") || strings.Contains(strings.ToLower(exePath), "go-build") {
		return nil
	}

	appData := os.Getenv("APPDATA")
	if appData == "" {
		return nil
	}
	startupDir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	batPath := filepath.Join(startupDir, "ReverseLocal-Startup.cmd")

	if enable {
		content := fmt.Sprintf("@echo off\r\nstart \"\" \"%s\"\r\n", exePath)
		return os.WriteFile(batPath, []byte(content), 0644)
	}

	_ = os.Remove(batPath)
	return nil
}
