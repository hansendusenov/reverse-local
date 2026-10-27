package settings

import (
	"path/filepath"
	"testing"
)

func TestStoreDefaultsAndUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "settings.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	initial := store.Get()
	if initial.HTTPPort != 80 || initial.HTTPSPort != 443 || initial.BindAddress != "127.0.0.1" {
		t.Fatalf("unexpected default settings: %+v", initial)
	}
	if !initial.AutoSyncHosts || !initial.SSHAutoReconnect || initial.SSHKeepAliveSec != 15 {
		t.Fatalf("unexpected boolean/keepalive defaults: %+v", initial)
	}

	updated, err := store.Update(Settings{
		HTTPPort:         8080,
		HTTPSPort:        8443,
		BindAddress:      "0.0.0.0",
		AutoSyncHosts:    false,
		AutoStartOnBoot:  true,
		SSHKeepAliveSec:  30,
		SSHAutoReconnect: false,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if updated.HTTPPort != 8080 || updated.HTTPSPort != 8443 || updated.BindAddress != "0.0.0.0" {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}

	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatalf("reloading store failed: %v", err)
	}
	got := reloaded.Get()
	if got.HTTPPort != 8080 || got.HTTPSPort != 8443 || got.BindAddress != "0.0.0.0" || got.SSHKeepAliveSec != 30 {
		t.Fatalf("persisted settings mismatch: %+v", got)
	}
}

func TestStoreValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := NewStore(filepath.Join(tmpDir, "settings.json"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	cases := []struct {
		name string
		cfg  Settings
	}{
		{
			name: "invalid http port",
			cfg:  Settings{HTTPPort: 70000, HTTPSPort: 443, BindAddress: "127.0.0.1", SSHKeepAliveSec: 15},
		},
		{
			name: "duplicate ports",
			cfg:  Settings{HTTPPort: 443, HTTPSPort: 443, BindAddress: "127.0.0.1", SSHKeepAliveSec: 15},
		},
		{
			name: "invalid bind address",
			cfg:  Settings{HTTPPort: 80, HTTPSPort: 443, BindAddress: "192.168.1.99", SSHKeepAliveSec: 15},
		},
		{
			name: "keepalive too low",
			cfg:  Settings{HTTPPort: 80, HTTPSPort: 443, BindAddress: "127.0.0.1", SSHKeepAliveSec: 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Update(tc.cfg); err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
		})
	}
}

