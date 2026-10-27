package logger

import (
	"testing"
	"time"
)

func TestLoggerRingBufferAndStreaming(t *testing.T) {
	logStore := NewLogger(3) // Cap at 3 entries

	// Add 2 entries
	logStore.Add(LogEntry{Method: "GET", Host: "api.local", Path: "/v1/users", StatusCode: 200})
	logStore.Add(LogEntry{Method: "POST", Host: "auth.local", Path: "/login", StatusCode: 200})

	logs := logStore.GetAll(10, "")
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	if logs[0].Host != "auth.local" { // Newest first
		t.Errorf("expected newest entry first, got %s", logs[0].Host)
	}

	// Subscribe channel for live streaming
	ch, unsub := logStore.Subscribe()
	defer unsub()

	// Add 3rd and 4th entry to test ring buffer eviction
	logStore.Add(LogEntry{Method: "GET", Host: "web.local", Path: "/index", StatusCode: 200})
	logStore.Add(LogEntry{Method: "DELETE", Host: "api.local", Path: "/v1/users/1", StatusCode: 204})

	select {
	case entry := <-ch:
		if entry.Host != "web.local" {
			t.Errorf("expected streamed entry web.local, got %s", entry.Host)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timeout waiting for streamed entry")
	}

	// Verify buffer size did not exceed 3
	logs = logStore.GetAll(10, "")
	if len(logs) != 3 {
		t.Fatalf("expected 3 entries after ring eviction, got %d", len(logs))
	}

	// Verify filtering
	filtered := logStore.GetAll(10, "api.local")
	if len(filtered) != 1 || filtered[0].Path != "/v1/users/1" {
		t.Errorf("unexpected filter result: %+v", filtered)
	}

	// Test clear
	logStore.Clear()
	if len(logStore.GetAll(10, "")) != 0 {
		t.Errorf("expected 0 logs after clear")
	}
}
