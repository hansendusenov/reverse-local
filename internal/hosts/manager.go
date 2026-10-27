package hosts

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
)

const (
	BlockStart = "# BEGIN LOCAL-REVERSE-PROXY"
	BlockEnd   = "# END LOCAL-REVERSE-PROXY"
	LocalIP    = "127.0.0.1"
)

// Manager synchronizes local domains with the OS hosts file.
type Manager struct {
	mu   sync.Mutex
	path string
}

// DefaultHostsPath returns the default system hosts file path for the current OS.
func DefaultHostsPath() string {
	if runtime.GOOS == "windows" {
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		return systemRoot + `\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// NewManager creates a new Manager for the specified hosts path.
// If path is empty, DefaultHostsPath() is used.
func NewManager(path string) *Manager {
	if path == "" {
		path = DefaultHostsPath()
	}
	return &Manager{
		path: path,
	}
}

// Path returns the configured hosts file path.
func (m *Manager) Path() string {
	return m.path
}

// Sync updates the managed block in the hosts file with the given list of active domains.
// It guarantees that "reverse.local" is always included.
func (m *Manager) Sync(domains []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure reverse.local is included and deduplicate
	domainSet := make(map[string]bool)
	domainSet["reverse.local"] = true
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			domainSet[d] = true
		}
	}

	// Sort domain list with reverse.local first
	sortedDomains := make([]string, 0, len(domainSet))
	sortedDomains = append(sortedDomains, "reverse.local")
	otherDomains := make([]string, 0, len(domainSet)-1)
	for d := range domainSet {
		if d != "reverse.local" {
			otherDomains = append(otherDomains, d)
		}
	}
	sort.Strings(otherDomains)
	sortedDomains = append(sortedDomains, otherDomains...)

	content, err := m.readHostsFile()
	if err != nil {
		return err
	}

	eol := detectLineEnding(content)
	newBlock := formatBlock(sortedDomains, eol)
	updatedContent := updateHostsContent(content, newBlock, eol)

	return m.writeHostsFile(updatedContent)
}

// Clean removes the managed block from the hosts file completely.
func (m *Manager) Clean() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	content, err := m.readHostsFile()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	eol := detectLineEnding(content)
	updatedContent := removeBlockFromContent(content, eol)

	// Only write if changes were made
	if updatedContent != content {
		return m.writeHostsFile(updatedContent)
	}
	return nil
}

func (m *Manager) readHostsFile() (string, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsPermission(err) {
			return "", fmt.Errorf("insufficient permissions to read hosts file (%s): %w", m.path, err)
		}
		return "", err
	}
	return string(data), nil
}

func (m *Manager) writeHostsFile(content string) error {
	err := os.WriteFile(m.path, []byte(content), 0644)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("insufficient permissions to write to hosts file (%s). Please run as Administrator on Windows or sudo on Linux/macOS: %w", m.path, err)
		}
		return fmt.Errorf("failed to write hosts file (%s): %w", m.path, err)
	}
	return nil
}

// formatBlock generates the text for the managed block.
func formatBlock(domains []string, eol string) string {
	var lines []string
	lines = append(lines, BlockStart)
	for _, d := range domains {
		lines = append(lines, fmt.Sprintf("%s %s", LocalIP, d))
	}
	lines = append(lines, BlockEnd)
	return strings.Join(lines, eol)
}

// detectLineEnding detects CRLF or LF in the content, defaulting to OS default.
func detectLineEnding(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}

// updateHostsContent replaces the managed block or appends it.
func updateHostsContent(content, newBlock, eol string) string {
	startIndex := strings.Index(content, BlockStart)
	if startIndex != -1 {
		endIndex := strings.Index(content, BlockEnd)
		if endIndex != -1 && endIndex >= startIndex {
			// Find where BlockEnd finishes
			endIndex += len(BlockEnd)
			before := content[:startIndex]
			after := content[endIndex:]
			// Trim empty trailing spaces from replacement junction
			return strings.TrimRight(before, " \t\r\n") + eol + newBlock + eol + strings.TrimLeft(after, " \t\r\n")
		}
	}

	// Block not found, append to end
	trimmed := strings.TrimRight(content, " \t\r\n")
	if trimmed == "" {
		return newBlock + eol
	}
	return trimmed + eol + eol + newBlock + eol
}

// removeBlockFromContent removes the managed block from the hosts content.
func removeBlockFromContent(content, eol string) string {
	startIndex := strings.Index(content, BlockStart)
	if startIndex == -1 {
		return content
	}

	endIndex := strings.Index(content, BlockEnd)
	if endIndex == -1 || endIndex < startIndex {
		// Malformed end tag: remove from start tag to end of file
		return strings.TrimRight(content[:startIndex], " \t\r\n") + eol
	}

	endIndex += len(BlockEnd)
	before := strings.TrimRight(content[:startIndex], " \t\r\n")
	after := strings.TrimLeft(content[endIndex:], " \t\r\n")

	if before == "" && after == "" {
		return ""
	}
	if before == "" {
		return after + eol
	}
	if after == "" {
		return before + eol
	}

	return before + eol + eol + after + eol
}
