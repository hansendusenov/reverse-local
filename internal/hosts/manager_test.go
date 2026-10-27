package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostsManager(t *testing.T) {
	tempDir := t.TempDir()
	hostsPath := filepath.Join(tempDir, "hosts")

	initialContent := `# Custom user hosts file
127.0.0.1 localhost
::1 localhost
192.168.1.50 myserver
`
	if err := os.WriteFile(hostsPath, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to write initial hosts: %v", err)
	}

	mgr := NewManager(hostsPath)

	// Test 1: Sync first time
	err := mgr.Sync([]string{"api.local", "frontend.local"})
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	data, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("failed to read hosts: %v", err)
	}
	content := string(data)

	// Must contain original entries
	if !strings.Contains(content, "192.168.1.50 myserver") {
		t.Errorf("original hosts content was lost: %s", content)
	}

	// Must contain block markers and reverse.local + added domains
	if !strings.Contains(content, BlockStart) || !strings.Contains(content, BlockEnd) {
		t.Errorf("missing block markers: %s", content)
	}
	if !strings.Contains(content, "127.0.0.1 reverse.local") {
		t.Errorf("missing reverse.local in block: %s", content)
	}
	if !strings.Contains(content, "127.0.0.1 api.local") || !strings.Contains(content, "127.0.0.1 frontend.local") {
		t.Errorf("missing added domains in block: %s", content)
	}

	// Test 2: Update domains (remove frontend.local, add dashboard.local)
	err = mgr.Sync([]string{"api.local", "dashboard.local"})
	if err != nil {
		t.Fatalf("second sync failed: %v", err)
	}

	data, _ = os.ReadFile(hostsPath)
	content = string(data)
	if strings.Contains(content, "127.0.0.1 frontend.local") {
		t.Errorf("expected frontend.local to be removed from block")
	}
	if !strings.Contains(content, "127.0.0.1 dashboard.local") {
		t.Errorf("expected dashboard.local to be present in block")
	}
	// Verify markers appear only once
	if strings.Count(content, BlockStart) != 1 || strings.Count(content, BlockEnd) != 1 {
		t.Errorf("expected exactly 1 instance of block markers, got %d start and %d end",
			strings.Count(content, BlockStart), strings.Count(content, BlockEnd))
	}

	// Test 3: Clean
	err = mgr.Clean()
	if err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	data, _ = os.ReadFile(hostsPath)
	content = string(data)
	if strings.Contains(content, BlockStart) || strings.Contains(content, BlockEnd) {
		t.Errorf("block markers still present after Clean(): %s", content)
	}
	if strings.Contains(content, "reverse.local") {
		t.Errorf("reverse.local still present after Clean(): %s", content)
	}
	if !strings.Contains(content, "192.168.1.50 myserver") {
		t.Errorf("original custom entries were wiped during Clean(): %s", content)
	}
}
