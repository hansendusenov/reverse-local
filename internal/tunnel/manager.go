package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var (
	ErrTunnelNotFound   = errors.New("SSH tunnel profile not found")
	ErrDuplicatePort    = errors.New("local forwarding port is already assigned to another tunnel")
	ErrInvalidConfig    = errors.New("invalid SSH tunnel configuration")
	ErrSecretRequired   = errors.New("password or key passphrase is required to connect")
	ErrPrivateKeyNotSet = errors.New("private key path is required for key authentication")
)

// TunnelConfig represents a persisted SSH local port forwarding (-L -N) profile.
type TunnelConfig struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SSHHost        string `json:"ssh_host"`
	SSHPort        int    `json:"ssh_port"`
	SSHUser        string `json:"ssh_user"`
	AuthType       string `json:"auth_type"` // "key", "password", "agent"
	PrivateKeyPath string `json:"private_key_path"`
	Secret         string `json:"secret,omitempty"`
	SaveSecret     bool   `json:"save_secret"`
	LocalAddr      string `json:"local_addr"`
	LocalPort      int    `json:"local_port"`
	RemoteHost     string `json:"remote_host"`
	RemotePort     int    `json:"remote_port"`
	AutoConnect    bool   `json:"auto_connect"`
	CreatedAt      string `json:"created_at"`
}

// TunnelView combines configuration (with secret redacted) and live runtime telemetry.
type TunnelView struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	SSHHost           string `json:"ssh_host"`
	SSHPort           int    `json:"ssh_port"`
	SSHUser           string `json:"ssh_user"`
	AuthType          string `json:"auth_type"`
	PrivateKeyPath    string `json:"private_key_path"`
	SaveSecret        bool   `json:"save_secret"`
	HasSavedSecret    bool   `json:"has_saved_secret"`
	LocalAddr         string `json:"local_addr"`
	LocalPort         int    `json:"local_port"`
	RemoteHost        string `json:"remote_host"`
	RemotePort        int    `json:"remote_port"`
	AutoConnect       bool   `json:"auto_connect"`
	CreatedAt         string `json:"created_at"`
	Status            string `json:"status"` // "stopped", "connecting", "connected", "reconnecting", "error"
	LastError         string `json:"last_error"`
	ActiveConns       int64  `json:"active_conns"`
	TotalConns        int64  `json:"total_conns"`
	BytesIn           int64  `json:"bytes_in"`
	BytesOut          int64  `json:"bytes_out"`
	ConnectedAt       string `json:"connected_at"`
	CommandEquivalent string `json:"command_equivalent"`
}

type runtimeState struct {
	mu          sync.Mutex
	status      string
	lastError   string
	connectedAt string
	cancel      context.CancelFunc
	listener    net.Listener
	sshClient   *ssh.Client
	activeConns atomic.Int64
	totalConns  atomic.Int64
	bytesIn     atomic.Int64
	bytesOut    atomic.Int64
}

// Manager coordinates persistence and active runtime sessions for native Go SSH tunnels.
type Manager struct {
	mu            sync.RWMutex
	filePath      string
	tunnels       map[string]TunnelConfig
	states        map[string]*runtimeState
	keepAliveSec  int
	autoReconnect bool
	dialTimeout   time.Duration
}

// DefaultTunnelsPath returns the default path for tunnels.json in the user's config directory.
func DefaultTunnelsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "tunnels.json"
	}
	return filepath.Join(configDir, "reverse-local", "tunnels.json")
}

// NewManager loads SSH tunnel profiles from disk and initializes the runtime manager.
func NewManager(filePath string) (*Manager, error) {
	if filePath == "" {
		filePath = DefaultTunnelsPath()
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tunnel config directory %s: %w", dir, err)
	}

	m := &Manager{
		filePath:      filePath,
		tunnels:       make(map[string]TunnelConfig),
		states:        make(map[string]*runtimeState),
		keepAliveSec:  15,
		autoReconnect: true,
		dialTimeout:   8 * time.Second,
	}

	if err := m.load(); err != nil {
		return nil, err
	}

	return m, nil
}

// SetPolicies updates runtime keepalive and auto-reconnect settings.
func (m *Manager) SetPolicies(keepAliveSec int, autoReconnect bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if keepAliveSec >= 5 && keepAliveSec <= 300 {
		m.keepAliveSec = keepAliveSec
	}
	m.autoReconnect = autoReconnect
}

func (m *Manager) load() error {
	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		return m.saveLocked()
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", m.filePath, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return m.saveLocked()
	}

	var list []TunnelConfig
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("failed to parse tunnels JSON: %w", err)
	}

	m.tunnels = make(map[string]TunnelConfig, len(list))
	for _, t := range list {
		m.tunnels[t.ID] = t
		m.states[t.ID] = &runtimeState{status: "stopped"}
	}

	return nil
}

func (m *Manager) saveLocked() error {
	list := make([]TunnelConfig, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		persisted := t
		if !persisted.SaveSecret {
			persisted.Secret = ""
		}
		list = append(list, persisted)
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tunnels: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", m.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp tunnels file: %w", err)
	}

	if err := os.Rename(tmpFile, m.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to replace tunnels file: %w", err)
	}

	return nil
}

// GetAll returns all configured SSH tunnels along with their live runtime telemetry.
func (m *Manager) GetAll() []TunnelView {
	m.mu.RLock()
	defer m.mu.RUnlock()

	views := make([]TunnelView, 0, len(m.tunnels))
	for id, cfg := range m.tunnels {
		st := m.states[id]
		views = append(views, buildView(cfg, st))
	}

	sort.Slice(views, func(i, j int) bool {
		if views[i].LocalPort != views[j].LocalPort {
			return views[i].LocalPort < views[j].LocalPort
		}
		return views[i].Name < views[j].Name
	})

	return views
}

// Counts returns (connectedTunnels, totalTunnels).
func (m *Manager) Counts() (int, int) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	connected := 0
	for _, st := range m.states {
		st.mu.Lock()
		if st.status == "connected" {
			connected++
		}
		st.mu.Unlock()
	}
	return connected, len(m.tunnels)
}

// ExportConfigs returns all saved tunnel configurations for backup export (with secrets redacted unless SaveSecret is true).
func (m *Manager) ExportConfigs() []TunnelConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]TunnelConfig, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		persisted := t
		if !persisted.SaveSecret {
			persisted.Secret = ""
		}
		list = append(list, persisted)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// ImportConfigs merges and persists imported SSH tunnel profiles.
func (m *Manager) ImportConfigs(incoming []TunnelConfig) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	imported := 0
	for _, item := range incoming {
		clean, err := sanitizeAndValidate(item)
		if err != nil {
			continue
		}
		var matchID string
		for id, existing := range m.tunnels {
			if existing.LocalPort == clean.LocalPort || strings.EqualFold(existing.Name, clean.Name) {
				matchID = id
				break
			}
		}
		if matchID != "" {
			clean.ID = matchID
			if clean.Secret == "" && m.tunnels[matchID].Secret != "" {
				clean.Secret = m.tunnels[matchID].Secret
			}
			m.tunnels[matchID] = clean
			if _, ok := m.states[matchID]; !ok {
				m.states[matchID] = &runtimeState{status: "stopped"}
			}
		} else {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				id = generateTunnelID()
			}
			if _, exists := m.tunnels[id]; exists {
				id = generateTunnelID()
			}
			clean.ID = id
			clean.CreatedAt = time.Now().UTC().Format(time.RFC3339)
			m.tunnels[id] = clean
			m.states[id] = &runtimeState{status: "stopped"}
		}
		imported++
	}

	if err := m.saveLocked(); err != nil {
		return 0, err
	}
	return imported, nil
}

// Add validates and persists a new SSH tunnel profile.
func (m *Manager) Add(cfg TunnelConfig) (TunnelView, error) {
	clean, err := sanitizeAndValidate(cfg)
	if err != nil {
		return TunnelView{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, existing := range m.tunnels {
		if existing.LocalPort == clean.LocalPort {
			return TunnelView{}, ErrDuplicatePort
		}
	}

	clean.ID = generateTunnelID()
	clean.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if !clean.SaveSecret {
		clean.Secret = ""
	}

	st := &runtimeState{status: "stopped"}
	m.tunnels[clean.ID] = clean
	m.states[clean.ID] = st

	if err := m.saveLocked(); err != nil {
		delete(m.tunnels, clean.ID)
		delete(m.states, clean.ID)
		return TunnelView{}, err
	}

	return buildView(clean, st), nil
}

// Update modifies an existing tunnel profile. If the tunnel is currently running, it is stopped first.
func (m *Manager) Update(id string, cfg TunnelConfig) (TunnelView, error) {
	clean, err := sanitizeAndValidate(cfg)
	if err != nil {
		return TunnelView{}, err
	}

	_ = m.Stop(id)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.tunnels[id]
	if !ok {
		return TunnelView{}, ErrTunnelNotFound
	}

	for otherID, other := range m.tunnels {
		if otherID != id && other.LocalPort == clean.LocalPort {
			return TunnelView{}, ErrDuplicatePort
		}
	}

	clean.ID = id
	clean.CreatedAt = existing.CreatedAt
	if clean.SaveSecret && clean.Secret == "" && existing.Secret != "" {
		clean.Secret = existing.Secret
	}
	if !clean.SaveSecret {
		clean.Secret = ""
	}

	m.tunnels[id] = clean
	st := m.states[id]
	if st == nil {
		st = &runtimeState{status: "stopped"}
		m.states[id] = st
	}

	if err := m.saveLocked(); err != nil {
		return TunnelView{}, err
	}

	return buildView(clean, st), nil
}

// Delete stops a tunnel if active and removes its profile from disk.
func (m *Manager) Delete(id string) error {
	_ = m.Stop(id)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tunnels[id]; !ok {
		return ErrTunnelNotFound
	}

	delete(m.tunnels, id)
	delete(m.states, id)
	return m.saveLocked()
}

// Start establishes the SSH connection and binds the local forwarding port (-L -N).
func (m *Manager) Start(id string, runtimeSecret string) (TunnelView, error) {
	m.mu.RLock()
	cfg, ok := m.tunnels[id]
	st := m.states[id]
	keepAliveSec := m.keepAliveSec
	autoReconnect := m.autoReconnect
	dialTimeout := m.dialTimeout
	m.mu.RUnlock()

	if !ok || st == nil {
		return TunnelView{}, ErrTunnelNotFound
	}

	// Stop any existing session before starting fresh
	_ = m.Stop(id)

	secret := strings.TrimSpace(runtimeSecret)
	if secret == "" {
		secret = cfg.Secret
	}

	sshClientCfg, err := buildSSHClientConfig(cfg, secret, dialTimeout)
	if err != nil {
		st.mu.Lock()
		st.status = "error"
		st.lastError = err.Error()
		st.mu.Unlock()
		return buildView(cfg, st), err
	}

	localBind := fmt.Sprintf("%s:%d", cfg.LocalAddr, cfg.LocalPort)
	listener, err := net.Listen("tcp", localBind)
	if err != nil {
		errMsg := fmt.Sprintf("failed to bind local port %s: %v", localBind, err)
		st.mu.Lock()
		st.status = "error"
		st.lastError = errMsg
		st.mu.Unlock()
		return buildView(cfg, st), errors.New(errMsg)
	}

	st.mu.Lock()
	st.status = "connecting"
	st.lastError = ""
	st.mu.Unlock()

	sshAddr := fmt.Sprintf("%s:%d", cfg.SSHHost, cfg.SSHPort)
	client, err := ssh.Dial("tcp", sshAddr, sshClientCfg)
	if err != nil {
		_ = listener.Close()
		errMsg := fmt.Sprintf("SSH handshake to %s failed: %v", sshAddr, err)
		st.mu.Lock()
		st.status = "error"
		st.lastError = errMsg
		st.mu.Unlock()
		return buildView(cfg, st), errors.New(errMsg)
	}

	ctx, cancel := context.WithCancel(context.Background())

	st.mu.Lock()
	st.status = "connected"
	st.lastError = ""
	st.connectedAt = time.Now().UTC().Format(time.RFC3339)
	st.cancel = cancel
	st.listener = listener
	st.sshClient = client
	st.mu.Unlock()

	go m.superviseTunnel(ctx, cfg, st, sshClientCfg, keepAliveSec, autoReconnect)

	return buildView(cfg, st), nil
}

func (m *Manager) superviseTunnel(
	ctx context.Context,
	cfg TunnelConfig,
	st *runtimeState,
	sshClientCfg *ssh.ClientConfig,
	keepAliveSec int,
	autoReconnect bool,
) {
	sshAddr := fmt.Sprintf("%s:%d", cfg.SSHHost, cfg.SSHPort)
	remoteTarget := fmt.Sprintf("%s:%d", cfg.RemoteHost, cfg.RemotePort)

	st.mu.Lock()
	listener := st.listener
	currentClient := st.sshClient
	st.mu.Unlock()

	// Accept loop for local connections
	go func() {
		for {
			localConn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}

			st.mu.Lock()
			activeSSH := st.sshClient
			st.mu.Unlock()

			if activeSSH == nil {
				_ = localConn.Close()
				continue
			}

			go forwardStream(ctx, localConn, activeSSH, remoteTarget, st)
		}
	}()

	// Monitor SSH session keepalive and reconnect if dropped
	backoff := 2 * time.Second
	for {
		sessionErr := runKeepAliveLoop(ctx, currentClient, time.Duration(keepAliveSec)*time.Second)
		select {
		case <-ctx.Done():
			return
		default:
		}

		st.mu.Lock()
		if st.sshClient != nil {
			_ = st.sshClient.Close()
			st.sshClient = nil
		}
		if !autoReconnect {
			st.status = "error"
			if sessionErr != nil {
				st.lastError = fmt.Sprintf("SSH session closed: %v", sessionErr)
			} else {
				st.lastError = "SSH session disconnected"
			}
			if st.listener != nil {
				_ = st.listener.Close()
				st.listener = nil
			}
			st.mu.Unlock()
			return
		}
		st.status = "reconnecting"
		if sessionErr != nil {
			st.lastError = fmt.Sprintf("Connection lost (%v); reconnecting...", sessionErr)
		}
		st.mu.Unlock()

		// Reconnection loop
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			newClient, err := ssh.Dial("tcp", sshAddr, sshClientCfg)
			if err != nil {
				st.mu.Lock()
				st.lastError = fmt.Sprintf("Reconnect attempt failed: %v", err)
				st.mu.Unlock()
				if backoff < 16*time.Second {
					backoff *= 2
				}
				continue
			}

			st.mu.Lock()
			st.sshClient = newClient
			st.status = "connected"
			st.lastError = ""
			st.connectedAt = time.Now().UTC().Format(time.RFC3339)
			st.mu.Unlock()

			currentClient = newClient
			backoff = 2 * time.Second
			break
		}
	}
}

func runKeepAliveLoop(ctx context.Context, client *ssh.Client, interval time.Duration) error {
	if interval < 5*time.Second {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- client.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-waitCh:
			return err
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				return err
			}
		}
	}
}

func forwardStream(ctx context.Context, localConn net.Conn, client *ssh.Client, remoteTarget string, st *runtimeState) {
	st.activeConns.Add(1)
	st.totalConns.Add(1)
	defer func() {
		st.activeConns.Add(-1)
		_ = localConn.Close()
	}()

	remoteConn, err := client.Dial("tcp", remoteTarget)
	if err != nil {
		st.mu.Lock()
		st.lastError = fmt.Sprintf("Remote target %s refused tunnel stream: %v", remoteTarget, err)
		st.mu.Unlock()
		return
	}
	defer func() {
		_ = remoteConn.Close()
	}()

	done := make(chan struct{}, 2)

	// Local -> Remote (BytesOut)
	go func() {
		_, _ = io.Copy(&atomicCounterWriter{w: remoteConn, counter: &st.bytesOut}, localConn)
		done <- struct{}{}
	}()

	// Remote -> Local (BytesIn)
	go func() {
		_, _ = io.Copy(&atomicCounterWriter{w: localConn, counter: &st.bytesIn}, remoteConn)
		done <- struct{}{}
	}()

	select {
	case <-ctx.Done():
	case <-done:
	}
}

type atomicCounterWriter struct {
	w       io.Writer
	counter *atomic.Int64
}

func (a *atomicCounterWriter) Write(p []byte) (int, error) {
	n, err := a.w.Write(p)
	if n > 0 {
		a.counter.Add(int64(n))
	}
	return n, err
}

// Stop terminates an active SSH tunnel and releases its local port listener.
func (m *Manager) Stop(id string) error {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()

	if !ok || st == nil {
		return ErrTunnelNotFound
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	if st.listener != nil {
		_ = st.listener.Close()
		st.listener = nil
	}
	if st.sshClient != nil {
		_ = st.sshClient.Close()
		st.sshClient = nil
	}
	st.status = "stopped"
	st.lastError = ""
	st.connectedAt = ""
	return nil
}

// StartAutoConnect launches all tunnel profiles configured with AutoConnect == true.
func (m *Manager) StartAutoConnect() {
	m.mu.RLock()
	ids := make([]string, 0)
	for id, cfg := range m.tunnels {
		if cfg.AutoConnect {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_, _ = m.Start(id, "")
	}
}

// StopAll terminates all active SSH tunnels cleanly.
func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.tunnels))
	for id := range m.tunnels {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.Stop(id)
	}
}

func buildSSHClientConfig(cfg TunnelConfig, secret string, timeout time.Duration) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	switch cfg.AuthType {
	case "password":
		if secret == "" {
			return nil, ErrSecretRequired
		}
		authMethods = append(authMethods,
			ssh.Password(secret),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = secret
				}
				return answers, nil
			}),
		)

	case "agent":
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if conn, err := net.Dial("unix", sock); err == nil {
				agentClient := agent.NewClient(conn)
				authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
			}
		}
		// Also try default user key files as helpful fallback
		if signer, err := loadPrivateKeySigner(cfg.PrivateKeyPath, secret); err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
		if len(authMethods) == 0 {
			return nil, errors.New("no SSH agent socket (SSH_AUTH_SOCK) or default private key found")
		}

	default: // "key"
		signer, err := loadPrivateKeySigner(cfg.PrivateKeyPath, secret)
		if err != nil {
			return nil, err
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	return &ssh.ClientConfig{
		User:            cfg.SSHUser,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}, nil
}

func loadPrivateKeySigner(keyPath, passphrase string) (ssh.Signer, error) {
	resolved := expandHomePath(strings.TrimSpace(keyPath))
	if resolved == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, ErrPrivateKeyNotSet
		}
		ed25519Path := filepath.Join(home, ".ssh", "id_ed25519")
		rsaPath := filepath.Join(home, ".ssh", "id_rsa")
		if _, err := os.Stat(ed25519Path); err == nil {
			resolved = ed25519Path
		} else if _, err := os.Stat(rsaPath); err == nil {
			resolved = rsaPath
		} else {
			return nil, ErrPrivateKeyNotSet
		}
	}

	pemBytes, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key %s: %w", resolved, err)
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		var passErr *ssh.PassphraseMissingError
		if errors.As(err, &passErr) {
			if passphrase == "" {
				return nil, ErrSecretRequired
			}
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
			if err != nil {
				return nil, fmt.Errorf("invalid private key passphrase: %w", err)
			}
			return signer, nil
		}
		if passphrase != "" {
			if s, pErr := ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase)); pErr == nil {
				return s, nil
			}
		}
		return nil, fmt.Errorf("failed to parse private key %s: %w", resolved, err)
	}

	return signer, nil
}

func expandHomePath(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func sanitizeAndValidate(cfg TunnelConfig) (TunnelConfig, error) {
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.SSHHost = strings.TrimSpace(cfg.SSHHost)
	cfg.SSHUser = strings.TrimSpace(cfg.SSHUser)
	cfg.AuthType = strings.ToLower(strings.TrimSpace(cfg.AuthType))
	cfg.PrivateKeyPath = strings.TrimSpace(cfg.PrivateKeyPath)
	cfg.LocalAddr = strings.TrimSpace(cfg.LocalAddr)
	cfg.RemoteHost = strings.TrimSpace(cfg.RemoteHost)

	if cfg.SSHPort == 0 {
		cfg.SSHPort = 22
	}
	if cfg.LocalAddr == "" {
		cfg.LocalAddr = "127.0.0.1"
	}
	if cfg.RemoteHost == "" {
		cfg.RemoteHost = "127.0.0.1"
	}
	if cfg.AuthType == "" {
		cfg.AuthType = "key"
	}

	if cfg.Name == "" || cfg.SSHHost == "" || cfg.SSHUser == "" {
		return TunnelConfig{}, fmt.Errorf("%w: name, ssh_host, and ssh_user are required", ErrInvalidConfig)
	}
	if cfg.SSHPort < 1 || cfg.SSHPort > 65535 {
		return TunnelConfig{}, fmt.Errorf("%w: ssh_port must be 1..65535", ErrInvalidConfig)
	}
	if cfg.LocalPort < 1 || cfg.LocalPort > 65535 {
		return TunnelConfig{}, fmt.Errorf("%w: local_port must be 1..65535", ErrInvalidConfig)
	}
	if cfg.RemotePort < 1 || cfg.RemotePort > 65535 {
		return TunnelConfig{}, fmt.Errorf("%w: remote_port must be 1..65535", ErrInvalidConfig)
	}
	if cfg.AuthType != "key" && cfg.AuthType != "password" && cfg.AuthType != "agent" {
		return TunnelConfig{}, fmt.Errorf("%w: auth_type must be key, password, or agent", ErrInvalidConfig)
	}

	return cfg, nil
}

func buildView(cfg TunnelConfig, st *runtimeState) TunnelView {
	status := "stopped"
	lastErr := ""
	connectedAt := ""
	var activeConns, totalConns, bytesIn, bytesOut int64

	if st != nil {
		st.mu.Lock()
		status = st.status
		lastErr = st.lastError
		connectedAt = st.connectedAt
		st.mu.Unlock()

		activeConns = st.activeConns.Load()
		totalConns = st.totalConns.Load()
		bytesIn = st.bytesIn.Load()
		bytesOut = st.bytesOut.Load()
	}

	cmd := fmt.Sprintf("ssh -N -L %s:%d:%s:%d -p %d %s@%s",
		cfg.LocalAddr, cfg.LocalPort, cfg.RemoteHost, cfg.RemotePort, cfg.SSHPort, cfg.SSHUser, cfg.SSHHost)

	return TunnelView{
		ID:                cfg.ID,
		Name:              cfg.Name,
		SSHHost:           cfg.SSHHost,
		SSHPort:           cfg.SSHPort,
		SSHUser:           cfg.SSHUser,
		AuthType:          cfg.AuthType,
		PrivateKeyPath:    cfg.PrivateKeyPath,
		SaveSecret:        cfg.SaveSecret,
		HasSavedSecret:    cfg.Secret != "",
		LocalAddr:         cfg.LocalAddr,
		LocalPort:         cfg.LocalPort,
		RemoteHost:        cfg.RemoteHost,
		RemotePort:        cfg.RemotePort,
		AutoConnect:       cfg.AutoConnect,
		CreatedAt:         cfg.CreatedAt,
		Status:            status,
		LastError:         lastErr,
		ActiveConns:       activeConns,
		TotalConns:        totalConns,
		BytesIn:           bytesIn,
		BytesOut:          bytesOut,
		ConnectedAt:       connectedAt,
		CommandEquivalent: cmd,
	}
}

func generateTunnelID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

