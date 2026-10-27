package proxy

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"reverse-local/internal/assets"
	"reverse-local/internal/certs"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/lan"
	"reverse-local/internal/logger"
	"reverse-local/internal/settings"
	"reverse-local/internal/tunnel"
	"reverse-local/web"
)

// Server coordinates the HTTP (80) and HTTPS (443) reverse proxy, SSH tunnels, LAN DNS broadcaster, and REST API.
type Server struct {
	store          *config.Store
	hostsManager   *hosts.Manager
	caManager      *certs.CAManager
	settingsStore  *settings.Store
	tunnelManager  *tunnel.Manager
	lanBroadcaster *lan.Broadcaster
	logger         *logger.Logger
	httpPort       int
	httpsPort      int
	bindAddress    string

	stateMu      sync.RWMutex
	proxyEnabled bool
	isRunning    bool

	httpServer  *http.Server
	httpsServer *http.Server

	proxyMu    sync.RWMutex
	proxyCache map[string]*httputil.ReverseProxy

	apiMux http.Handler
}

// Config provides configuration options for the Server.
type Config struct {
	Store         *config.Store
	HostsManager  *hosts.Manager
	CAManager     *certs.CAManager
	SettingsStore *settings.Store
	TunnelManager *tunnel.Manager
	Logger        *logger.Logger
	HTTPPort      int
	HTTPSPort     int
}

// NewServer initializes the proxy, settings, SSH tunnel manager, and API server.
func NewServer(cfg Config) *Server {
	baseDir := ""
	if cfg.Store != nil && cfg.Store.FilePath() != "" {
		baseDir = filepath.Dir(cfg.Store.FilePath())
	}

	if cfg.SettingsStore == nil {
		settingsPath := ""
		if baseDir != "" {
			settingsPath = filepath.Join(baseDir, "settings.json")
		}
		if st, err := settings.NewStore(settingsPath); err == nil {
			cfg.SettingsStore = st
		}
	}

	if cfg.TunnelManager == nil {
		tunnelsPath := ""
		if baseDir != "" {
			tunnelsPath = filepath.Join(baseDir, "tunnels.json")
		}
		if tm, err := tunnel.NewManager(tunnelsPath); err == nil {
			cfg.TunnelManager = tm
		}
	}

	bindAddr := "127.0.0.1"
	if cfg.SettingsStore != nil {
		saved := cfg.SettingsStore.Get()
		if cfg.HTTPPort == 0 {
			cfg.HTTPPort = saved.HTTPPort
		}
		if cfg.HTTPSPort == 0 {
			cfg.HTTPSPort = saved.HTTPSPort
		}
		if saved.BindAddress != "" {
			bindAddr = saved.BindAddress
		}
		if cfg.TunnelManager != nil {
			cfg.TunnelManager.SetPolicies(saved.SSHKeepAliveSec, saved.SSHAutoReconnect)
		}
	}

	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}
	if cfg.Logger == nil {
		cfg.Logger = logger.NewLogger(1000)
	}

	s := &Server{
		store:         cfg.Store,
		hostsManager:  cfg.HostsManager,
		caManager:     cfg.CAManager,
		settingsStore: cfg.SettingsStore,
		tunnelManager: cfg.TunnelManager,
		logger:        cfg.Logger,
		httpPort:      cfg.HTTPPort,
		httpsPort:     cfg.HTTPSPort,
		bindAddress:   bindAddr,
		proxyEnabled:  true,
		proxyCache:    make(map[string]*httputil.ReverseProxy),
	}

	dnsPort := 53
	if s.settingsStore != nil && s.settingsStore.Get().LANDNSPort > 0 {
		dnsPort = s.settingsStore.Get().LANDNSPort
	}
	s.lanBroadcaster = lan.NewBroadcaster(func(domain string) bool {
		if s.store == nil {
			return false
		}
		r, found := s.store.GetByDomain(domain)
		return found && r.Active
	}, dnsPort)

	s.setupAPIRouter()
	return s
}

// Start begins listening on HTTP and HTTPS in background goroutines, starts LAN DNS/mDNS broadcast, and connects auto-start SSH tunnels.
func (s *Server) Start() error {
	// Sync active routes with hosts file on startup
	if err := s.syncHosts(); err != nil {
		log.Printf("[WARN] Initial hosts sync notice: %v", err)
	}

	if err := s.startListeners(); err != nil {
		return err
	}

	s.syncLANBroadcaster()

	if s.tunnelManager != nil {
		go s.tunnelManager.StartAutoConnect()
	}

	return nil
}

func (s *Server) syncLANBroadcaster() {
	if s.lanBroadcaster == nil {
		return
	}
	cfg := settings.DefaultSettings()
	if s.settingsStore != nil {
		cfg = s.settingsStore.Get()
	}
	if cfg.LANBroadcast {
		_ = s.lanBroadcaster.Start(true, cfg.LANDNSPort)
	} else {
		s.lanBroadcaster.Stop()
	}
}

func (s *Server) startListeners() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	bindPrefix := ""
	if s.bindAddress == "127.0.0.1" {
		bindPrefix = "127.0.0.1"
	}

	httpPort := s.httpPort
	httpsPort := s.httpsPort

	// 1. HTTP Server (Port 80) -> Redirect to HTTPS
	httpMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := stripPort(r.Host)
		var target string
		if httpsPort == 443 {
			target = fmt.Sprintf("https://%s%s", host, r.RequestURI)
		} else {
			target = fmt.Sprintf("https://%s:%d%s", host, httpsPort, r.RequestURI)
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindPrefix, httpPort),
		Handler:      httpMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// 2. HTTPS Server (Port 443) -> Dynamic SNI + Reverse Proxy
	tlsConfig := &tls.Config{
		GetCertificate: s.caManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}

	s.httpsServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindPrefix, httpsPort),
		Handler:      s,
		TLSConfig:    tlsConfig,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	s.isRunning = true

	// Start HTTP listener
	go func(srv *http.Server, addr string) {
		log.Printf("[INFO] HTTP server listening on %s (redirecting to HTTPS)", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] HTTP server error: %v", err)
		}
	}(s.httpServer, s.httpServer.Addr)

	// Start HTTPS listener
	go func(srv *http.Server, addr string) {
		log.Printf("[INFO] HTTPS reverse proxy listening on %s", addr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] HTTPS server error: %v", err)
		}
	}(s.httpsServer, s.httpsServer.Addr)

	return nil
}

// Stop gracefully shuts down both HTTP and HTTPS servers and all active SSH tunnels.
func (s *Server) Stop() {
	s.stateMu.Lock()
	s.isRunning = false
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.httpsServer != nil {
		_ = s.httpsServer.Close()
	}
	s.stateMu.Unlock()

	if s.lanBroadcaster != nil {
		s.lanBroadcaster.Stop()
	}
	if s.tunnelManager != nil {
		s.tunnelManager.StopAll()
	}
}

// HTTPSPort returns the currently configured HTTPS listening port.
func (s *Server) HTTPSPort() int {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.httpsPort
}

// TunnelManager returns the server's native Go SSH tunnel manager.
func (s *Server) TunnelManager() *tunnel.Manager {
	return s.tunnelManager
}

// IsProxyEnabled returns whether virtual-host reverse proxy forwarding is enabled.
func (s *Server) IsProxyEnabled() bool {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.proxyEnabled
}

// ServeHTTP handles incoming HTTPS requests, logs traffic, and routes requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	lrw := &loggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}

	host := strings.ToLower(stripPort(r.Host))
	targetURL := "internal"
	var routeErr string

	// 1. Reserved domain or localhost -> Serve Web UI & API
	if host == "reverse.local" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		s.apiMux.ServeHTTP(lrw, r)
	} else if !s.IsProxyEnabled() {
		routeErr = "Proxy forwarding paused"
		renderNotFoundPage(lrw, host, true)
	} else {
		// 2. User configured domain -> Reverse proxy
		route, found := s.store.GetByDomain(host)
		if !found {
			routeErr = "Domain not registered"
			renderNotFoundPage(lrw, host, false)
		} else if !route.Active {
			routeErr = "Route inactive"
			renderNotFoundPage(lrw, host, true)
		} else {
			targetURL = route.TargetURL
			s.forwardRequest(lrw, r, route)
		}
	}

	// Log proxied domain traffic and web UI visits, but exclude internal /api/ endpoints from polluting logs
	if host != "reverse.local" || !strings.HasPrefix(r.URL.Path, "/api/") {
		duration := time.Since(start).Milliseconds()
		proto := r.Proto
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			proto = "WebSocket"
		}

		errMsg := routeErr
		if errMsg == "" && lrw.errorMsg != "" {
			errMsg = lrw.errorMsg
		}

		s.logger.Add(logger.LogEntry{
			ClientIP:   getClientIP(r),
			Method:     r.Method,
			Host:       r.Host,
			Path:       r.URL.RequestURI(),
			TargetURL:  targetURL,
			StatusCode: lrw.statusCode,
			DurationMs: duration,
			BytesSent:  lrw.bytesWritten,
			Proto:      proto,
			UserAgent:  r.UserAgent(),
			Error:      errMsg,
		})

		// Print concise access log to stdout
		if host != "reverse.local" {
			if errMsg != "" {
				log.Printf("[ACCESS] %d %s %s%s -> %s (%s, %dms)", lrw.statusCode, r.Method, r.Host, r.URL.RequestURI(), targetURL, errMsg, duration)
			} else {
				log.Printf("[ACCESS] %d %s %s%s -> %s (%dms)", lrw.statusCode, r.Method, r.Host, r.URL.RequestURI(), targetURL, duration)
			}
		}
	}
}

// forwardRequest proxies traffic to the configured target URL.
func (s *Server) forwardRequest(w *loggingResponseWriter, r *http.Request, route config.Route) {
	proxy := s.getOrCreateProxy(route.TargetURL)
	if proxy == nil {
		err := fmt.Errorf("invalid target URL: %s", route.TargetURL)
		w.errorMsg = err.Error()
		renderBadGatewayPage(w, route.Domain, route.TargetURL, err)
		return
	}

	proxy.ServeHTTP(w, r)
}

func (s *Server) getOrCreateProxy(targetURLStr string) *httputil.ReverseProxy {
	s.proxyMu.RLock()
	if p, ok := s.proxyCache[targetURLStr]; ok {
		s.proxyMu.RUnlock()
		return p
	}
	s.proxyMu.RUnlock()

	target, err := url.Parse(targetURLStr)
	if err != nil || target.Host == "" {
		return nil
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director

	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		// Set target host so upstream server virtual hosts match
		req.Host = target.Host

		// Standard reverse proxy headers
		req.Header.Set("X-Forwarded-Host", rHeader(req, "Host", target.Host))
		req.Header.Set("X-Forwarded-Proto", "https")

		clientIP := getClientIP(req)
		if clientIP != "" {
			req.Header.Set("X-Real-IP", clientIP)
			if prior, ok := req.Header["X-Forwarded-For"]; ok {
				clientIP = strings.Join(prior, ", ") + ", " + clientIP
			}
			req.Header.Set("X-Forwarded-For", clientIP)
		}
	}

	// Flush immediately to support real-time streaming and WebSockets
	proxy.FlushInterval = 50 * time.Millisecond

	proxy.ErrorHandler = func(w http.ResponseWriter, req *http.Request, err error) {
		if lrw, ok := w.(*loggingResponseWriter); ok {
			lrw.errorMsg = err.Error()
		}
		renderBadGatewayPage(w, req.Host, targetURLStr, err)
	}

	s.proxyMu.Lock()
	s.proxyCache[targetURLStr] = proxy
	s.proxyMu.Unlock()

	return proxy
}

func (s *Server) setupAPIRouter() {
	mux := http.NewServeMux()

	// 1. Dashboard Web UI, Desktop Control Panel & CA download
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		indexHTML, err := web.Content.ReadFile("index.html")
		if err != nil {
			http.Error(w, "Dashboard UI not found", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		_, _ = w.Write(indexHTML)
	})

	mux.HandleFunc("GET /control", func(w http.ResponseWriter, r *http.Request) {
		controlHTML, err := web.Content.ReadFile("control.html")
		if err != nil {
			http.Error(w, "Control Panel UI not found", http.StatusInternalServerError)
			return
		}
		settings.EnforceFixedControlPanelWindow()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		_, _ = w.Write(controlHTML)
	})

	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		_, icoBytes := assets.GetAppIcons()
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(icoBytes)
	})

	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, r *http.Request) {
		pngBytes, _ := assets.GetAppIcons()
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(pngBytes)
	})

	mux.HandleFunc("GET /ca.crt", func(w http.ResponseWriter, r *http.Request) {
		pemBytes := s.caManager.CACertPEM()
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="reverse-local-ca.crt"`)
		_, _ = w.Write(pemBytes)
	})

	// 2. REST API endpoints (Status, Routes, CA)
	mux.HandleFunc("GET /api/status", s.handleGetStatus)
	mux.HandleFunc("GET /api/routes", s.handleGetRoutes)
	mux.HandleFunc("POST /api/routes", s.handleCreateRoute)
	mux.HandleFunc("PUT /api/routes/{id}", s.handleUpdateRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", s.handleDeleteRoute)
	mux.HandleFunc("POST /api/routes/{id}/toggle", s.handleToggleRoute)
	mux.HandleFunc("POST /api/ca/install", s.handleInstallCA)

	// 3. Daemon Settings & System Actions endpoints
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
	mux.HandleFunc("POST /api/system/action", s.handleSystemAction)

	// 4. Native Go SSH Tunnels (-L -N) endpoints
	mux.HandleFunc("GET /api/tunnels", s.handleGetTunnels)
	mux.HandleFunc("POST /api/tunnels", s.handleCreateTunnel)
	mux.HandleFunc("PUT /api/tunnels/{id}", s.handleUpdateTunnel)
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.handleDeleteTunnel)
	mux.HandleFunc("POST /api/tunnels/{id}/start", s.handleStartTunnel)
	mux.HandleFunc("POST /api/tunnels/{id}/stop", s.handleStopTunnel)

	// 5. Configuration Backup & Restore endpoints
	mux.HandleFunc("GET /api/backup", s.handleExportBackup)
	mux.HandleFunc("POST /api/backup/restore", s.handleRestoreBackup)

	// 6. Access Logs endpoints
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("DELETE /api/logs", s.handleClearLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleStreamLogs)

	s.apiMux = mux
}

// REST API Handlers

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	activeCount := len(s.store.GetActiveDomains())
	totalCount := len(s.store.GetAll())

	activeTunnels := 0
	totalTunnels := 0
	if s.tunnelManager != nil {
		activeTunnels, totalTunnels = s.tunnelManager.Counts()
	}

	s.stateMu.RLock()
	httpPort := s.httpPort
	httpsPort := s.httpsPort
	bindAddr := s.bindAddress
	proxyEnabled := s.proxyEnabled
	s.stateMu.RUnlock()

	configDir := ""
	if s.settingsStore != nil {
		configDir = s.settingsStore.ConfigDir()
	} else if s.store != nil {
		configDir = filepath.Dir(s.store.FilePath())
	}

	hostsPath := ""
	if s.hostsManager != nil {
		hostsPath = s.hostsManager.Path()
	}

	lanIP := lan.DetectLANIP()
	lanActive := false
	lanQueries := uint64(0)
	if s.lanBroadcaster != nil {
		lanIP = s.lanBroadcaster.LANIP()
		lanActive = s.lanBroadcaster.IsRunning()
		lanQueries = s.lanBroadcaster.QueriesAnswered()
	}
	lanEnabled := true
	lanPort := 53
	if s.settingsStore != nil {
		st := s.settingsStore.Get()
		lanEnabled = st.LANBroadcast
		lanPort = st.LANDNSPort
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":               "running",
		"proxy_enabled":        proxyEnabled,
		"bind_address":         bindAddr,
		"http_port":            httpPort,
		"https_port":           httpsPort,
		"ca_installed":         s.caManager.IsInstalled(),
		"active_routes":        activeCount,
		"total_routes":         totalCount,
		"active_tunnels":       activeTunnels,
		"total_tunnels":        totalTunnels,
		"lan_ip":               lanIP,
		"lan_broadcast":        lanEnabled,
		"lan_broadcast_active": lanActive,
		"lan_dns_port":         lanPort,
		"lan_dns_queries":      lanQueries,
		"config_dir":           configDir,
		"hosts_path":           hostsPath,
		"os":                   runtime.GOOS,
	})
}

func (s *Server) handleGetRoutes(w http.ResponseWriter, r *http.Request) {
	routes := s.store.GetAll()
	respondJSON(w, http.StatusOK, routes)
}

func (s *Server) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Domain    string `json:"domain"`
		TargetURL string `json:"target_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	route, err := s.store.Add(input.Domain, input.TargetURL)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	_ = s.syncHosts()
	respondJSON(w, http.StatusCreated, route)
}

func (s *Server) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Route ID is required")
		return
	}

	var input struct {
		Domain    string `json:"domain"`
		TargetURL string `json:"target_url"`
		Active    bool   `json:"active"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	route, err := s.store.Update(id, input.Domain, input.TargetURL, input.Active)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	_ = s.syncHosts()
	respondJSON(w, http.StatusOK, route)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Route ID is required")
		return
	}

	if err := s.store.Delete(id); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	_ = s.syncHosts()
	respondJSON(w, http.StatusOK, map[string]string{"message": "Route deleted successfully"})
}

func (s *Server) handleToggleRoute(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Route ID is required")
		return
	}

	route, err := s.store.Toggle(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	_ = s.syncHosts()
	respondJSON(w, http.StatusOK, route)
}

func (s *Server) handleInstallCA(w http.ResponseWriter, r *http.Request) {
	err := s.caManager.InstallRootCA()
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Root CA installed successfully into OS trust store",
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		respondJSON(w, http.StatusOK, settings.DefaultSettings())
		return
	}
	respondJSON(w, http.StatusOK, s.settingsStore.Get())
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		respondError(w, http.StatusInternalServerError, "Settings store not initialized")
		return
	}

	var input settings.Settings
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	updated, err := s.settingsStore.Update(input)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.tunnelManager != nil {
		s.tunnelManager.SetPolicies(updated.SSHKeepAliveSec, updated.SSHAutoReconnect)
	}

	s.stateMu.Lock()
	needsListenerReload := s.isRunning && (s.httpPort != updated.HTTPPort || s.httpsPort != updated.HTTPSPort || s.bindAddress != updated.BindAddress)
	s.httpPort = updated.HTTPPort
	s.httpsPort = updated.HTTPSPort
	s.bindAddress = updated.BindAddress
	if needsListenerReload {
		if s.httpServer != nil {
			_ = s.httpServer.Close()
		}
		if s.httpsServer != nil {
			_ = s.httpsServer.Close()
		}
	}
	s.stateMu.Unlock()

	if needsListenerReload {
		_ = s.startListeners()
	}

	s.syncLANBroadcaster()

	if updated.AutoSyncHosts {
		_ = s.forceSyncHosts()
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"settings": updated,
		"message":  "Daemon settings saved and applied",
	})
}

func (s *Server) handleExportBackup(w http.ResponseWriter, r *http.Request) {
	var routes []config.Route
	if s.store != nil {
		routes = s.store.GetAll()
	}
	var tunnels []tunnel.TunnelConfig
	if s.tunnelManager != nil {
		tunnels = s.tunnelManager.ExportConfigs()
	}
	cfg := settings.DefaultSettings()
	if s.settingsStore != nil {
		cfg = s.settingsStore.Get()
	}

	payload := map[string]interface{}{
		"version":     "1.0",
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"settings":    cfg,
		"routes":      routes,
		"tunnels":     tunnels,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="reverse-local-backup.json"`)
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Settings *settings.Settings    `json:"settings"`
		Routes   []config.Route        `json:"routes"`
		Tunnels  []tunnel.TunnelConfig `json:"tunnels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid backup JSON file")
		return
	}

	routesImported := 0
	tunnelsImported := 0

	if s.store != nil && len(payload.Routes) > 0 {
		n, err := s.store.ReplaceAll(payload.Routes)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to import routes: %v", err))
			return
		}
		routesImported = n
		_ = s.syncHosts()
	}

	if s.tunnelManager != nil && len(payload.Tunnels) > 0 {
		n, err := s.tunnelManager.ImportConfigs(payload.Tunnels)
		if err != nil {
			respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to import SSH tunnels: %v", err))
			return
		}
		tunnelsImported = n
	}

	if s.settingsStore != nil && payload.Settings != nil {
		if updated, err := s.settingsStore.Update(*payload.Settings); err == nil {
			if s.tunnelManager != nil {
				s.tunnelManager.SetPolicies(updated.SSHKeepAliveSec, updated.SSHAutoReconnect)
			}
			s.syncLANBroadcaster()
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"routes_imported":  routesImported,
		"tunnels_imported": tunnelsImported,
		"message":          fmt.Sprintf("Restored %d domain route(s) and %d SSH tunnel profile(s) to disk.", routesImported, tunnelsImported),
	})
}

func (s *Server) handleSystemAction(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	switch input.Action {
	case "toggle_proxy":
		s.stateMu.Lock()
		s.proxyEnabled = !s.proxyEnabled
		enabled := s.proxyEnabled
		s.stateMu.Unlock()

		stateLabel := "resumed"
		if !enabled {
			stateLabel = "paused"
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"proxy_enabled": enabled,
			"message":       fmt.Sprintf("HTTP/HTTPS reverse proxy forwarding %s.", stateLabel),
		})

	case "sync_hosts":
		if err := s.forceSyncHosts(); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": fmt.Sprintf("Synchronized %d active domain(s) with OS hosts file.", len(s.store.GetActiveDomains())),
		})

	case "flush_dns":
		out, err := settings.FlushDNSCache()
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": out,
		})

	case "open_workbench":
		urlStr := s.WorkbenchURL("")
		if err := settings.LaunchBrowser(urlStr); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": fmt.Sprintf("Opened Workbench (%s) in default browser.", urlStr),
		})

	case "open_control_panel":
		urlStr := s.WorkbenchURL("/control")
		if err := settings.LaunchControlPanelApp(urlStr); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Opened Desktop Control Panel window.",
		})

	case "open_config_dir":
		dir := ""
		if s.settingsStore != nil {
			dir = s.settingsStore.ConfigDir()
		} else if s.store != nil {
			dir = filepath.Dir(s.store.FilePath())
		}
		if err := settings.OpenPathInFileManager(dir); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": fmt.Sprintf("Opened config folder: %s", dir),
		})

	case "start_auto_tunnels":
		if s.tunnelManager != nil {
			go s.tunnelManager.StartAutoConnect()
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Triggered connection for auto-connect SSH tunnels.",
		})

	case "stop_all_tunnels":
		if s.tunnelManager != nil {
			s.tunnelManager.StopAll()
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message": "Stopped all active SSH port forwarding tunnels.",
		})

	default:
		respondError(w, http.StatusBadRequest, "Unsupported system action")
	}
}

func (s *Server) handleGetTunnels(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondJSON(w, http.StatusOK, []tunnel.TunnelView{})
		return
	}
	respondJSON(w, http.StatusOK, s.tunnelManager.GetAll())
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondError(w, http.StatusInternalServerError, "SSH tunnel manager not initialized")
		return
	}

	var input struct {
		tunnel.TunnelConfig
		ConnectNow bool `json:"connect_now"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	view, err := s.tunnelManager.Add(input.TunnelConfig)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if input.ConnectNow || input.AutoConnect {
		if startedView, startErr := s.tunnelManager.Start(view.ID, input.Secret); startErr == nil {
			view = startedView
		} else {
			// Profile was saved, include connection error in view
			view = startedView
		}
	}

	respondJSON(w, http.StatusCreated, view)
}

func (s *Server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondError(w, http.StatusInternalServerError, "SSH tunnel manager not initialized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Tunnel ID is required")
		return
	}

	var input tunnel.TunnelConfig
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON payload")
		return
	}

	view, err := s.tunnelManager.Update(id, input)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondError(w, http.StatusInternalServerError, "SSH tunnel manager not initialized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Tunnel ID is required")
		return
	}

	if err := s.tunnelManager.Delete(id); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "SSH tunnel deleted"})
}

func (s *Server) handleStartTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondError(w, http.StatusInternalServerError, "SSH tunnel manager not initialized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Tunnel ID is required")
		return
	}

	var input struct {
		Secret string `json:"secret"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&input)
	}

	view, err := s.tunnelManager.Start(id, input.Secret)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, tunnel.ErrSecretRequired) || errors.Is(err, tunnel.ErrPrivateKeyNotSet) {
			status = http.StatusBadRequest
		}
		respondJSON(w, status, map[string]interface{}{
			"message": err.Error(),
			"tunnel":  view,
		})
		return
	}

	respondJSON(w, http.StatusOK, view)
}

func (s *Server) handleStopTunnel(w http.ResponseWriter, r *http.Request) {
	if s.tunnelManager == nil {
		respondError(w, http.StatusInternalServerError, "SSH tunnel manager not initialized")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		respondError(w, http.StatusBadRequest, "Tunnel ID is required")
		return
	}

	if err := s.tunnelManager.Stop(id); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "SSH tunnel stopped"})
}

// WorkbenchURL returns the full URL to the workbench or a sub-path such as "/control".
func (s *Server) WorkbenchURL(subPath string) string {
	port := s.HTTPSPort()
	if port == 443 || port == 0 {
		return fmt.Sprintf("https://reverse.local%s", subPath)
	}
	return fmt.Sprintf("https://reverse.local:%d%s", port, subPath)
}

func (s *Server) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	search := r.URL.Query().Get("search")
	logs := s.logger.GetAll(limit, search)
	respondJSON(w, http.StatusOK, logs)
}

func (s *Server) handleClearLogs(w http.ResponseWriter, r *http.Request) {
	s.logger.Clear()
	respondJSON(w, http.StatusOK, map[string]string{"message": "Logs cleared successfully"})
}

func (s *Server) handleStreamLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	logChan, unsubscribe := s.logger.Subscribe()
	defer unsubscribe()

	// Send initial ping
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-logChan:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
				flusher.Flush()
			}
		}
	}
}

// syncHosts updates the OS hosts file with all active domains when auto_sync_hosts is enabled.
func (s *Server) syncHosts() error {
	if s.settingsStore != nil && !s.settingsStore.Get().AutoSyncHosts {
		return nil
	}
	return s.forceSyncHosts()
}

func (s *Server) forceSyncHosts() error {
	activeDomains := s.store.GetActiveDomains()
	return s.hostsManager.Sync(activeDomains)
}

// Helpers & Error Pages

func respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, statusCode int, message string) {
	respondJSON(w, statusCode, map[string]string{"message": message})
}

func stripPort(hostPort string) string {
	h, _, err := net.SplitHostPort(hostPort)
	if err == nil {
		return h
	}
	return hostPort
}

func getClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func rHeader(req *http.Request, key, fallback string) string {
	val := req.Header.Get(key)
	if val == "" {
		return fallback
	}
	return val
}

// loggingResponseWriter captures HTTP status code and bytes written while supporting Hijacker and Flusher.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	hijacked     bool
	errorMsg     string
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := lrw.ResponseWriter.Write(b)
	lrw.bytesWritten += int64(n)
	return n, err
}

func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		lrw.hijacked = true
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("underlying ResponseWriter does not support Hijacker")
}

func (lrw *loggingResponseWriter) Flush() {
	if flusher, ok := lrw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

const badGatewayHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>502 Bad Gateway - {{.Domain}}</title>
  <style>
    body { background-color: #fafafa; color: #18181b; font-family: "IBM Plex Sans", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100dvh; margin: 0; padding: 16px; box-sizing: border-box; }
    .card { background: #ffffff; border: 1px solid #e4e4e7; border-top: 4px solid #dc2626; padding: 28px; max-width: 520px; width: 100%; }
    .badge { display: inline-block; padding: 4px 8px; background: #fef2f2; border: 1px solid #dc2626; color: #b91c1c; font-family: "IBM Plex Mono", ui-monospace, monospace; font-size: 12px; font-weight: 600; margin-bottom: 16px; }
    h1 { margin: 0 0 8px 0; font-size: 20px; font-weight: 700; color: #18181b; letter-spacing: -0.01em; }
    p { color: #52525b; font-size: 14px; line-height: 1.55; margin: 0 0 16px 0; }
    .details { background: #f4f4f5; border: 1px solid #e4e4e7; padding: 12px; font-family: "IBM Plex Mono", ui-monospace, monospace; font-size: 12px; color: #0369a1; word-break: break-all; margin-bottom: 20px; line-height: 1.5; }
    .btn { display: inline-flex; align-items: center; min-height: 40px; padding: 8px 18px; background: #dc2626; color: #ffffff; text-decoration: none; font-family: "IBM Plex Mono", ui-monospace, monospace; font-weight: 700; font-size: 12px; text-transform: uppercase; box-sizing: border-box; }
    .btn:hover { background: #b91c1c; }
    .btn:focus-visible { outline: 2px solid #dc2626; outline-offset: 2px; }
  </style>
</head>
<body>
  <main class="card">
    <div class="badge">502 BAD GATEWAY</div>
    <h1>Target Service Unreachable</h1>
    <p>Reverse.Local could not connect to the upstream target configured for <strong>{{.Domain}}</strong>.</p>
    <div class="details">Target: {{.TargetURL}}<br>Error: {{.Error}}</div>
    <p>Start your local development server on that port, or update the upstream target port in the workbench.</p>
    <a href="https://reverse.local" class="btn">Open Reverse.Local Workbench</a>
  </main>
</body>
</html>`

func renderBadGatewayPage(w http.ResponseWriter, domain, targetURL string, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)

	tmpl, _ := template.New("502").Parse(badGatewayHTML)
	_ = tmpl.Execute(w, map[string]string{
		"Domain":    domain,
		"TargetURL": targetURL,
		"Error":     err.Error(),
	})
}

const notFoundHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>{{if .Inactive}}Domain Inactive{{else}}Domain Not Found{{end}} - {{.Domain}}</title>
  <style>
    body { background-color: #fafafa; color: #18181b; font-family: "IBM Plex Sans", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100dvh; margin: 0; padding: 16px; box-sizing: border-box; }
    .card { background: #ffffff; border: 1px solid #e4e4e7; border-top: 4px solid #dc2626; padding: 28px; max-width: 520px; width: 100%; }
    .badge { display: inline-block; padding: 4px 8px; background: #fef2f2; border: 1px solid #dc2626; color: #b91c1c; font-family: "IBM Plex Mono", ui-monospace, monospace; font-size: 12px; font-weight: 600; margin-bottom: 16px; }
    h1 { margin: 0 0 8px 0; font-size: 20px; font-weight: 700; color: #18181b; letter-spacing: -0.01em; }
    p { color: #52525b; font-size: 14px; line-height: 1.55; margin: 0 0 16px 0; }
    .domain { font-family: "IBM Plex Mono", ui-monospace, monospace; color: #b91c1c; font-weight: 600; }
    .btn { display: inline-flex; align-items: center; min-height: 40px; padding: 8px 18px; background: #dc2626; color: #ffffff; text-decoration: none; font-family: "IBM Plex Mono", ui-monospace, monospace; font-weight: 700; font-size: 12px; text-transform: uppercase; box-sizing: border-box; }
    .btn:hover { background: #b91c1c; }
    .btn:focus-visible { outline: 2px solid #dc2626; outline-offset: 2px; }
  </style>
</head>
<body>
  <main class="card">
    <div class="badge">{{if .Inactive}}503 ROUTE PAUSED{{else}}404 UNREGISTERED DOMAIN{{end}}</div>
    <h1>{{if .Inactive}}Route is Paused{{else}}Domain Not Registered{{end}}</h1>
    <p>Traffic for <span class="domain">{{.Domain}}</span> reached Reverse.Local, but this domain is {{if .Inactive}}currently paused{{else}}not registered in the routing matrix{{end}}.</p>
    <a href="https://reverse.local" class="btn">Configure Route in Workbench</a>
  </main>
</body>
</html>`

func renderNotFoundPage(w http.ResponseWriter, domain string, inactive bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if inactive {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}

	tmpl, _ := template.New("404").Parse(notFoundHTML)
	_ = tmpl.Execute(w, map[string]interface{}{
		"Domain":   domain,
		"Inactive": inactive,
	})
}
