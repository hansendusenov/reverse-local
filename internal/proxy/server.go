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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"reverse-local/internal/certs"
	"reverse-local/internal/config"
	"reverse-local/internal/hosts"
	"reverse-local/internal/logger"
	"reverse-local/web"
)

// Server coordinates the HTTP (80) and HTTPS (443) reverse proxy and REST API.
type Server struct {
	store        *config.Store
	hostsManager *hosts.Manager
	caManager    *certs.CAManager
	logger       *logger.Logger
	httpPort     int
	httpsPort    int

	httpServer  *http.Server
	httpsServer *http.Server

	proxyMu    sync.RWMutex
	proxyCache map[string]*httputil.ReverseProxy

	apiMux http.Handler
}

// Config provides configuration options for the Server.
type Config struct {
	Store        *config.Store
	HostsManager *hosts.Manager
	CAManager    *certs.CAManager
	Logger       *logger.Logger
	HTTPPort     int
	HTTPSPort    int
}

// NewServer initializes the proxy and API server.
func NewServer(cfg Config) *Server {
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
		store:        cfg.Store,
		hostsManager: cfg.HostsManager,
		caManager:    cfg.CAManager,
		logger:       cfg.Logger,
		httpPort:     cfg.HTTPPort,
		httpsPort:    cfg.HTTPSPort,
		proxyCache:   make(map[string]*httputil.ReverseProxy),
	}

	s.setupAPIRouter()
	return s
}

// Start begins listening on HTTP and HTTPS in background goroutines.
func (s *Server) Start() error {
	// Sync active routes with hosts file on startup
	if err := s.syncHosts(); err != nil {
		log.Printf("[WARN] Initial hosts sync notice: %v", err)
	}

	// 1. HTTP Server (Port 80) -> Redirect to HTTPS
	httpMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := stripPort(r.Host)
		var target string
		if s.httpsPort == 443 {
			target = fmt.Sprintf("https://%s%s", host, r.RequestURI)
		} else {
			target = fmt.Sprintf("https://%s:%d%s", host, s.httpsPort, r.RequestURI)
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.httpPort),
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
		Addr:         fmt.Sprintf(":%d", s.httpsPort),
		Handler:      s,
		TLSConfig:    tlsConfig,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// Start HTTP listener
	go func() {
		log.Printf("[INFO] HTTP server listening on :%d (redirecting to HTTPS)", s.httpPort)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] HTTP server error: %v", err)
		}
	}()

	// Start HTTPS listener
	go func() {
		log.Printf("[INFO] HTTPS reverse proxy listening on :%d", s.httpsPort)
		if err := s.httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Printf("[ERROR] HTTPS server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down both HTTP and HTTPS servers.
func (s *Server) Stop() {
	if s.httpServer != nil {
		_ = s.httpServer.Close()
	}
	if s.httpsServer != nil {
		_ = s.httpsServer.Close()
	}
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

	// 1. Dashboard Web UI & CA download
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
		_, _ = w.Write(indexHTML)
	})

	mux.HandleFunc("GET /ca.crt", func(w http.ResponseWriter, r *http.Request) {
		pemBytes := s.caManager.CACertPEM()
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="reverse-local-ca.crt"`)
		_, _ = w.Write(pemBytes)
	})

	// 2. REST API endpoints
	mux.HandleFunc("GET /api/status", s.handleGetStatus)
	mux.HandleFunc("GET /api/routes", s.handleGetRoutes)
	mux.HandleFunc("POST /api/routes", s.handleCreateRoute)
	mux.HandleFunc("PUT /api/routes/{id}", s.handleUpdateRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", s.handleDeleteRoute)
	mux.HandleFunc("POST /api/routes/{id}/toggle", s.handleToggleRoute)
	mux.HandleFunc("POST /api/ca/install", s.handleInstallCA)

	// 3. Access Logs endpoints
	mux.HandleFunc("GET /api/logs", s.handleGetLogs)
	mux.HandleFunc("DELETE /api/logs", s.handleClearLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleStreamLogs)

	s.apiMux = mux
}

// REST API Handlers

func (s *Server) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	activeCount := len(s.store.GetActiveDomains())
	totalCount := len(s.store.GetAll())

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "running",
		"http_port":     s.httpPort,
		"https_port":    s.httpsPort,
		"ca_installed":  s.caManager.IsInstalled(),
		"active_routes": activeCount,
		"total_routes":  totalCount,
		"os":            runtime.GOOS,
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

// syncHosts updates the OS hosts file with all active domains.
func (s *Server) syncHosts() error {
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
  <title>502 Bad Gateway - {{.Domain}}</title>
  <style>
    body { background-color: #0b0f19; color: #f1f5f9; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; }
    .card { background: #111827; border: 1px solid #1f2937; border-radius: 16px; padding: 32px; max-width: 520px; box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5); }
    .badge { display: inline-block; padding: 4px 10px; border-radius: 9999px; background: rgba(244, 63, 94, 0.1); color: #f43f5e; font-size: 12px; font-weight: 600; margin-bottom: 16px; }
    h1 { margin: 0 0 8px 0; font-size: 22px; font-weight: 700; color: #ffffff; }
    p { color: #94a3b8; font-size: 14px; line-height: 1.5; margin: 0 0 16px 0; }
    .details { background: #0f172a; border-radius: 8px; padding: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; color: #38bdf8; word-break: break-all; margin-bottom: 20px; }
    .btn { display: inline-block; padding: 10px 20px; border-radius: 8px; background: #10b981; color: #042f2e; text-decoration: none; font-weight: 600; font-size: 13px; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">502 Bad Gateway</div>
    <h1>Target Service Unreachable</h1>
    <p>Reverse Local could not connect to the upstream backend for <strong>{{.Domain}}</strong>.</p>
    <div class="details">Target: {{.TargetURL}}<br>Error: {{.Error}}</div>
    <p>Please ensure your local web server or development process is running and actively listening on that port.</p>
    <a href="https://reverse.local" class="btn">Manage in Reverse.Local</a>
  </div>
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
  <title>{{if .Inactive}}Domain Inactive{{else}}Domain Not Found{{end}} - {{.Domain}}</title>
  <style>
    body { background-color: #0b0f19; color: #f1f5f9; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; }
    .card { background: #111827; border: 1px solid #1f2937; border-radius: 16px; padding: 32px; max-width: 520px; box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.5); }
    .badge { display: inline-block; padding: 4px 10px; border-radius: 9999px; background: rgba(245, 158, 11, 0.1); color: #f59e0b; font-size: 12px; font-weight: 600; margin-bottom: 16px; }
    h1 { margin: 0 0 8px 0; font-size: 22px; font-weight: 700; color: #ffffff; }
    p { color: #94a3b8; font-size: 14px; line-height: 1.5; margin: 0 0 16px 0; }
    .domain { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: #34d399; font-weight: 600; }
    .btn { display: inline-block; padding: 10px 20px; border-radius: 8px; background: #10b981; color: #042f2e; text-decoration: none; font-weight: 600; font-size: 13px; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">{{if .Inactive}}Inactive Route{{else}}Unregistered Domain{{end}}</div>
    <h1>{{if .Inactive}}Route is Inactive{{else}}Domain Not Configured{{end}}</h1>
    <p>Traffic for <span class="domain">{{.Domain}}</span> reached this local proxy, but this domain is {{if .Inactive}}currently toggled off{{else}}not yet registered{{end}}.</p>
    <a href="https://reverse.local" class="btn">Configure at reverse.local</a>
  </div>
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
