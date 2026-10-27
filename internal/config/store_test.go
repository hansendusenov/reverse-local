package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreCRUD(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "routes.json")

	store, err := NewStore(cfgPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Initial empty check
	if len(store.GetAll()) != 0 {
		t.Errorf("expected 0 routes, got %d", len(store.GetAll()))
	}

	// Add route
	route, err := store.Add("myapp.local", "http://127.0.0.1:3000")
	if err != nil {
		t.Fatalf("unexpected error adding route: %v", err)
	}
	if route.Domain != "myapp.local" || route.TargetURL != "http://127.0.0.1:3000" || !route.Active {
		t.Errorf("route fields mismatch: %+v", route)
	}

	// Duplicate domain check
	_, err = store.Add("myapp.local", "http://127.0.0.1:4000")
	if err != ErrDomainDuplicate {
		t.Errorf("expected ErrDomainDuplicate, got %v", err)
	}

	// Reserved domain check
	_, err = store.Add("reverse.local", "http://127.0.0.1:8080")
	if err != ErrDomainReserved {
		t.Errorf("expected ErrDomainReserved, got %v", err)
	}

	// Toggle
	toggled, err := store.Toggle(route.ID)
	if err != nil {
		t.Fatalf("failed to toggle route: %v", err)
	}
	if toggled.Active {
		t.Errorf("expected route to be inactive after toggle")
	}

	// Active domains should now be empty
	active := store.GetActiveDomains()
	if len(active) != 0 {
		t.Errorf("expected 0 active domains, got %v", active)
	}

	// Toggle back
	toggled, _ = store.Toggle(route.ID)
	if !toggled.Active {
		t.Errorf("expected route to be active after second toggle")
	}
	active = store.GetActiveDomains()
	if len(active) != 1 || active[0] != "myapp.local" {
		t.Errorf("expected active domain [myapp.local], got %v", active)
	}

	// Reload from disk to verify persistence
	reloaded, err := NewStore(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload store from disk: %v", err)
	}
	r, found := reloaded.Get(route.ID)
	if !found || r.Domain != "myapp.local" {
		t.Errorf("persisted route not found or corrupted: %+v", r)
	}

	// Delete
	if err := store.Delete(route.ID); err != nil {
		t.Fatalf("failed to delete route: %v", err)
	}
	if len(store.GetAll()) != 0 {
		t.Errorf("expected 0 routes after delete")
	}

	// Verify delete persisted
	reloadedAfterDel, _ := NewStore(cfgPath)
	if len(reloadedAfterDel.GetAll()) != 0 {
		t.Errorf("expected 0 routes in persisted file after delete")
	}
	_ = os.Remove(cfgPath)
}
