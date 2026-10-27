package logger

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// LogEntry represents an HTTP/HTTPS proxy access log event.
type LogEntry struct {
	ID         string `json:"id"`
	Timestamp  string `json:"timestamp"`
	ClientIP   string `json:"client_ip"`
	Method     string `json:"method"`
	Host       string `json:"host"`
	Path       string `json:"path"`
	TargetURL  string `json:"target_url"`
	StatusCode int    `json:"status_code"`
	DurationMs int64  `json:"duration_ms"`
	BytesSent  int64  `json:"bytes_sent"`
	Proto      string `json:"proto"`
	UserAgent  string `json:"user_agent"`
	Error      string `json:"error,omitempty"`
}

// Logger stores a thread-safe ring buffer of recent access logs and broadcasts new logs.
type Logger struct {
	mu          sync.RWMutex
	entries     []LogEntry
	maxEntries  int
	subscribers map[chan LogEntry]struct{}
}

// NewLogger initializes an in-memory logger ring buffer.
func NewLogger(maxEntries int) *Logger {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	return &Logger{
		entries:     make([]LogEntry, 0, maxEntries),
		maxEntries:  maxEntries,
		subscribers: make(map[chan LogEntry]struct{}),
	}
}

// Add logs a new request entry, appends to the ring buffer, and notifies active SSE subscribers.
func (l *Logger) Add(entry LogEntry) {
	if entry.ID == "" {
		entry.ID = generateID()
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	l.mu.Lock()
	if len(l.entries) >= l.maxEntries {
		// Evict oldest entry (first item)
		l.entries = append(l.entries[1:], entry)
	} else {
		l.entries = append(l.entries, entry)
	}

	// Fan-out to subscribers
	subs := make([]chan LogEntry, 0, len(l.subscribers))
	for ch := range l.subscribers {
		subs = append(subs, ch)
	}
	l.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
			// Non-blocking drop if channel is full
		}
	}
}

// GetAll returns recent logs in reverse chronological order (newest first).
func (l *Logger) GetAll(limit int, query string) []LogEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	total := len(l.entries)
	if total == 0 {
		return []LogEntry{}
	}

	query = strings.ToLower(strings.TrimSpace(query))
	results := make([]LogEntry, 0, total)

	for i := total - 1; i >= 0; i-- {
		item := l.entries[i]
		if query != "" {
			match := strings.Contains(strings.ToLower(item.Host), query) ||
				strings.Contains(strings.ToLower(item.Path), query) ||
				strings.Contains(strings.ToLower(item.Method), query) ||
				strings.Contains(strings.ToLower(item.TargetURL), query)
			if !match {
				continue
			}
		}
		results = append(results, item)
		if limit > 0 && len(results) >= limit {
			break
		}
	}

	return results
}

// Clear removes all stored log entries.
func (l *Logger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]LogEntry, 0, l.maxEntries)
}

// Subscribe registers a new subscriber channel for real-time log streaming (SSE).
// Returns the channel and an unsubscribe cleanup function.
func (l *Logger) Subscribe() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 100)

	l.mu.Lock()
	l.subscribers[ch] = struct{}{}
	l.mu.Unlock()

	unsubscribe := func() {
		l.mu.Lock()
		delete(l.subscribers, ch)
		close(ch)
		l.mu.Unlock()
	}

	return ch, unsubscribe
}

func generateID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
