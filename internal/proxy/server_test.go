package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"reverse-local/internal/certs"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
)

func setupTestServer(t *testing.T) (*Server, *config.Store, *hosts.Manager, *certs.CAManager) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "routes.json")
	hostsPath := filepath.Join(tempDir, "hosts")
	certDir := filepath.Join(tempDir, "certs")

	store, err := config.NewStore(cfgPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	hostsMgr := hosts.NewManager(hostsPath)
	caMgr, err := certs.NewCAManager(certDir)
	if err != nil {
		t.Fatalf("failed to create caMgr: %v", err)
	}

	srv := NewServer(Config{
		Store:        store,
		HostsManager: hostsMgr,
		CAManager:    caMgr,
		HTTPPort:     8080,
		HTTPSPort:    8443,
	})

	return srv, store, hostsMgr, caMgr
}

func TestAPIRoutesCRUD(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. Initial GET /api/routes -> empty
	req := httptest.NewRequest("GET", "https://reverse.local/api/routes", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var routes []config.Route
	if err := json.Unmarshal(w.Body.Bytes(), &routes); err != nil {
		t.Fatalf("failed to parse routes JSON: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes initially, got %d", len(routes))
	}

	// 2. POST /api/routes -> Create
	payload := []byte(`{"domain":"testapp.local","target_url":"http://127.0.0.1:3000"}`)
	req = httptest.NewRequest("POST", "https://reverse.local/api/routes", bytes.NewBuffer(payload))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var created config.Route
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to parse created route: %v", err)
	}
	if created.Domain != "testapp.local" || created.TargetURL != "http://127.0.0.1:3000" || !created.Active {
		t.Errorf("created route mismatch: %+v", created)
	}

	// 3. POST /api/routes/{id}/toggle
	req = httptest.NewRequest("POST", fmt.Sprintf("https://reverse.local/api/routes/%s/toggle", created.ID), nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on toggle, got %d", w.Code)
	}
	var toggled config.Route
	_ = json.Unmarshal(w.Body.Bytes(), &toggled)
	if toggled.Active {
		t.Errorf("expected route to be toggled to inactive")
	}

	// 4. PUT /api/routes/{id}
	updatePayload := []byte(`{"domain":"testapp.local","target_url":"http://127.0.0.1:4000","active":true}`)
	req = httptest.NewRequest("PUT", fmt.Sprintf("https://reverse.local/api/routes/%s", created.ID), bytes.NewBuffer(updatePayload))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on update, got %d", w.Code)
	}
	var updated config.Route
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.TargetURL != "http://127.0.0.1:4000" || !updated.Active {
		t.Errorf("updated route mismatch: %+v", updated)
	}

	// 5. GET /api/status
	req = httptest.NewRequest("GET", "https://reverse.local/api/status", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on status, got %d", w.Code)
	}
	var status map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &status)
	if status["active_routes"].(float64) != 1 {
		t.Errorf("expected 1 active route in status, got %v", status["active_routes"])
	}

	// 6. DELETE /api/routes/{id}
	req = httptest.NewRequest("DELETE", fmt.Sprintf("https://reverse.local/api/routes/%s", created.ID), nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete, got %d", w.Code)
	}

	// Verify route is gone
	req = httptest.NewRequest("GET", "https://reverse.local/api/routes", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	var finalRoutes []config.Route
	_ = json.Unmarshal(w.Body.Bytes(), &finalRoutes)
	if len(finalRoutes) != 0 {
		t.Errorf("expected 0 routes after delete, got %d", len(finalRoutes))
	}
}

func TestReverseProxyForwarding(t *testing.T) {
	srv, store, _, _ := setupTestServer(t)

	// Spin up mock backend HTTP server
	backendReceivedHost := ""
	backendReceivedProto := ""
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendReceivedHost = r.Host
		backendReceivedProto = r.Header.Get("X-Forwarded-Proto")
		w.Header().Set("X-Custom-Backend", "Success")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello from backend!"))
	}))
	defer mockBackend.Close()

	// Register domain in store pointing to mock backend
	_, err := store.Add("myservice.local", mockBackend.URL)
	if err != nil {
		t.Fatalf("failed to add route: %v", err)
	}

	// Make request through proxy
	req := httptest.NewRequest("GET", "https://myservice.local/test-endpoint", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from backend, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "Hello from backend!" {
		t.Errorf("expected 'Hello from backend!', got %q", w.Body.String())
	}
	if w.Header().Get("X-Custom-Backend") != "Success" {
		t.Errorf("backend header not received in proxy response")
	}
	if backendReceivedProto != "https" {
		t.Errorf("expected X-Forwarded-Proto 'https', got %q", backendReceivedProto)
	}
	if backendReceivedHost == "" {
		t.Errorf("expected non-empty backend received host")
	}
}

func TestUnregisteredDomainHandling(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "https://unknown.local/path", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown domain, got %d", w.Code)
	}
}

func TestDashboardAndCACertServing(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// GET /
	req := httptest.NewRequest("GET", "https://reverse.local/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for dashboard UI, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("reverse.local")) {
		t.Errorf("dashboard HTML did not contain 'reverse.local'")
	}

	// GET /ca.crt
	req = httptest.NewRequest("GET", "https://reverse.local/ca.crt", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for ca.crt, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("BEGIN CERTIFICATE")) {
		t.Errorf("expected PEM certificate in ca.crt response")
	}
}

func TestLogsEndpointAndCapture(t *testing.T) {
	srv, store, _, _ := setupTestServer(t)

	// Mock backend
	mockBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockBackend.Close()

	_, _ = store.Add("logtest.local", mockBackend.URL)

	// Fire an HTTP request to logtest.local
	req := httptest.NewRequest("GET", "https://logtest.local/api/ping?test=1", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Now check GET /api/logs
	reqLogs := httptest.NewRequest("GET", "https://reverse.local/api/logs", nil)
	wLogs := httptest.NewRecorder()
	srv.ServeHTTP(wLogs, reqLogs)

	if wLogs.Code != http.StatusOK {
		t.Fatalf("expected 200 from /api/logs, got %d", wLogs.Code)
	}

	var logList []map[string]interface{}
	if err := json.Unmarshal(wLogs.Body.Bytes(), &logList); err != nil {
		t.Fatalf("failed to parse logs JSON: %v", err)
	}
	if len(logList) == 0 {
		t.Fatalf("expected at least 1 log entry recorded, got 0")
	}

	// Check fields
	found := false
	for _, entry := range logList {
		if entry["host"] == "logtest.local" && entry["path"] == "/api/ping?test=1" {
			found = true
			if entry["status_code"].(float64) != 200 {
				t.Errorf("expected status code 200 in log, got %v", entry["status_code"])
			}
			break
		}
	}
	if !found {
		t.Errorf("logged entry for logtest.local not found in logs: %+v", logList)
	}

	// Test DELETE /api/logs
	reqClear := httptest.NewRequest("DELETE", "https://reverse.local/api/logs", nil)
	wClear := httptest.NewRecorder()
	srv.ServeHTTP(wClear, reqClear)

	if wClear.Code != http.StatusOK {
		t.Errorf("expected 200 from DELETE /api/logs, got %d", wClear.Code)
	}

	// Verify logs are empty
	reqLogsAfter := httptest.NewRequest("GET", "https://reverse.local/api/logs", nil)
	wLogsAfter := httptest.NewRecorder()
	srv.ServeHTTP(wLogsAfter, reqLogsAfter)
	var clearedLogs []map[string]interface{}
	_ = json.Unmarshal(wLogsAfter.Body.Bytes(), &clearedLogs)
	if len(clearedLogs) != 0 {
		t.Errorf("expected 0 logs after clear, got %d", len(clearedLogs))
	}
}

func TestControlPanelSettingsAndTunnelsAPI(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	// 1. GET /control
	reqCtrl := httptest.NewRequest("GET", "https://reverse.local/control", nil)
	wCtrl := httptest.NewRecorder()
	srv.ServeHTTP(wCtrl, reqCtrl)
	if wCtrl.Code != http.StatusOK || !bytes.Contains(wCtrl.Body.Bytes(), []byte("RL//CTRL")) {
		t.Fatalf("expected 200 and RL//CTRL from /control, got %d", wCtrl.Code)
	}

	// 2. GET & PUT /api/settings
	reqSet := httptest.NewRequest("PUT", "https://reverse.local/api/settings", bytes.NewBufferString(`{
		"http_port": 8080,
		"https_port": 8443,
		"bind_address": "0.0.0.0",
		"auto_sync_hosts": true,
		"ssh_keepalive_sec": 25,
		"ssh_auto_reconnect": true
	}`))
	wSet := httptest.NewRecorder()
	srv.ServeHTTP(wSet, reqSet)
	if wSet.Code != http.StatusOK {
		t.Fatalf("expected 200 from PUT /api/settings, got %d: %s", wSet.Code, wSet.Body.String())
	}

	// 3. POST /api/system/action (toggle_proxy)
	reqAct := httptest.NewRequest("POST", "https://reverse.local/api/system/action", bytes.NewBufferString(`{"action":"toggle_proxy"}`))
	wAct := httptest.NewRecorder()
	srv.ServeHTTP(wAct, reqAct)
	if wAct.Code != http.StatusOK || srv.IsProxyEnabled() {
		t.Fatalf("expected proxy to be paused after toggle_proxy, got %d", wAct.Code)
	}

	// 4. POST, GET, DELETE /api/tunnels
	tunnelPayload := `{
		"name": "Dev DB Forward",
		"ssh_host": "bastion.dev.local",
		"ssh_port": 22,
		"ssh_user": "deploy",
		"auth_type": "key",
		"private_key_path": "~/.ssh/id_ed25519",
		"local_addr": "127.0.0.1",
		"local_port": 15432,
		"remote_host": "127.0.0.1",
		"remote_port": 5432
	}`
	reqTun := httptest.NewRequest("POST", "https://reverse.local/api/tunnels", bytes.NewBufferString(tunnelPayload))
	wTun := httptest.NewRecorder()
	srv.ServeHTTP(wTun, reqTun)
	if wTun.Code != http.StatusCreated {
		t.Fatalf("expected 201 from POST /api/tunnels, got %d: %s", wTun.Code, wTun.Body.String())
	}

	var createdTun map[string]interface{}
	_ = json.Unmarshal(wTun.Body.Bytes(), &createdTun)
	tunID := createdTun["id"].(string)

	reqDelTun := httptest.NewRequest("DELETE", fmt.Sprintf("https://reverse.local/api/tunnels/%s", tunID), nil)
	wDelTun := httptest.NewRecorder()
	srv.ServeHTTP(wDelTun, reqDelTun)
	if wDelTun.Code != http.StatusOK {
		t.Fatalf("expected 200 from DELETE /api/tunnels/{id}, got %d", wDelTun.Code)
	}

	// 5. GET /api/backup & POST /api/backup/restore
	reqExport := httptest.NewRequest("GET", "https://reverse.local/api/backup", nil)
	wExport := httptest.NewRecorder()
	srv.ServeHTTP(wExport, reqExport)
	if wExport.Code != http.StatusOK {
		t.Fatalf("expected 200 from GET /api/backup, got %d", wExport.Code)
	}

	restorePayload := `{
		"routes": [{"domain": "restored.local", "target_url": "http://127.0.0.1:4000", "active": true}],
		"tunnels": [{
			"name": "Restored SSH",
			"ssh_host": "jump.example.com",
			"ssh_port": 22,
			"ssh_user": "root",
			"auth_type": "key",
			"private_key_path": "~/.ssh/id_ed25519",
			"local_addr": "127.0.0.1",
			"local_port": 16379,
			"remote_host": "127.0.0.1",
			"remote_port": 6379
		}]
	}`
	reqRestore := httptest.NewRequest("POST", "https://reverse.local/api/backup/restore", bytes.NewBufferString(restorePayload))
	wRestore := httptest.NewRecorder()
	srv.ServeHTTP(wRestore, reqRestore)
	if wRestore.Code != http.StatusOK {
		t.Fatalf("expected 200 from POST /api/backup/restore, got %d: %s", wRestore.Code, wRestore.Body.String())
	}
}
