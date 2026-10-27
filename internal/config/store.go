package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrRouteNotFound    = errors.New("route not found")
	ErrDomainReserved   = errors.New("domain 'reverse.local' is reserved for the dashboard")
	ErrDomainInvalid    = errors.New("invalid domain format")
	ErrDomainDuplicate  = errors.New("domain is already registered")
	ErrTargetURLInvalid = errors.New("invalid target URL (must include http:// or https:// and a host)")
)

var domainRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// Route represents a mapped local reverse proxy domain and its target.
type Route struct {
	ID        string `json:"id"`
	Domain    string `json:"domain"`     // e.g., "myapp.local"
	TargetURL string `json:"target_url"` // e.g., "http://127.0.0.1:3000"
	Active    bool   `json:"active"`     // true/false
	CreatedAt string `json:"created_at"`
}

// Store handles thread-safe persistence and queries for routes.
type Store struct {
	mu       sync.RWMutex
	filePath string
	routes   map[string]Route
}

// DefaultConfigPath returns the default path for routes.json in the user's config directory.
func DefaultConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "routes.json"
	}
	return filepath.Join(configDir, "reverse-local", "routes.json")
}

// NewStore loads routes from disk or initializes a new store.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = DefaultConfigPath()
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	s := &Store{
		filePath: filePath,
		routes:   make(map[string]Route),
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

// load reads the routes.json file into memory. Must be called with lock or during initialization.
func (s *Store) load() error {
	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// File does not exist yet; save empty list
		return s.saveLocked()
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", s.filePath, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return s.saveLocked()
	}

	var list []Route
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("failed to parse routes JSON: %w", err)
	}

	s.routes = make(map[string]Route, len(list))
	for _, r := range list {
		s.routes[r.ID] = r
	}

	return nil
}

// saveLocked writes the current in-memory routes to disk as JSON.
// It uses an atomic write pattern (temp file + rename) to protect against corruption.
func (s *Store) saveLocked() error {
	list := make([]Route, 0, len(s.routes))
	for _, r := range s.routes {
		list = append(list, r)
	}

	// Sort alphabetically by domain for clean JSON diffs
	sort.Slice(list, func(i, j int) bool {
		return list[i].Domain < list[j].Domain
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal routes: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", s.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to replace config file: %w", err)
	}

	return nil
}

// GetAll returns a copy of all registered routes, sorted by domain.
func (s *Store) GetAll() []Route {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Route, 0, len(s.routes))
	for _, r := range s.routes {
		result = append(result, r)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Domain < result[j].Domain
	})
	return result
}

// Get finds a route by its ID.
func (s *Store) Get(id string) (Route, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.routes[id]
	return r, ok
}

// GetByDomain finds an active route by domain name (case-insensitive).
func (s *Store) GetByDomain(domain string) (Route, bool) {
	normalized := strings.ToLower(strings.TrimSpace(domain))

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, r := range s.routes {
		if strings.ToLower(r.Domain) == normalized {
			return r, true
		}
	}
	return Route{}, false
}

// GetActiveDomains returns a list of unique domain names that are currently active.
func (s *Store) GetActiveDomains() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	domains := make([]string, 0)
	for _, r := range s.routes {
		if r.Active {
			domains = append(domains, strings.ToLower(r.Domain))
		}
	}
	sort.Strings(domains)
	return domains
}

// Add creates a new route after validation and persists it.
func (s *Store) Add(domain, targetURL string) (Route, error) {
	cleanDomain, cleanURL, err := validateInputs(domain, targetURL)
	if err != nil {
		return Route{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check duplicates
	for _, r := range s.routes {
		if strings.ToLower(r.Domain) == cleanDomain {
			return Route{}, ErrDomainDuplicate
		}
	}

	route := Route{
		ID:        generateID(),
		Domain:    cleanDomain,
		TargetURL: cleanURL,
		Active:    true,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	s.routes[route.ID] = route
	if err := s.saveLocked(); err != nil {
		delete(s.routes, route.ID)
		return Route{}, err
	}

	return route, nil
}

// Update modifies an existing route's domain, targetURL, or active status.
func (s *Store) Update(id, domain, targetURL string, active bool) (Route, error) {
	cleanDomain, cleanURL, err := validateInputs(domain, targetURL)
	if err != nil {
		return Route{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.routes[id]
	if !ok {
		return Route{}, ErrRouteNotFound
	}

	// Check if the domain is taken by another route
	for otherID, r := range s.routes {
		if otherID != id && strings.ToLower(r.Domain) == cleanDomain {
			return Route{}, ErrDomainDuplicate
		}
	}

	existing.Domain = cleanDomain
	existing.TargetURL = cleanURL
	existing.Active = active

	s.routes[id] = existing
	if err := s.saveLocked(); err != nil {
		return Route{}, err
	}

	return existing, nil
}

// Toggle flips the active status of a route.
func (s *Store) Toggle(id string) (Route, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.routes[id]
	if !ok {
		return Route{}, ErrRouteNotFound
	}

	existing.Active = !existing.Active
	s.routes[id] = existing

	if err := s.saveLocked(); err != nil {
		return Route{}, err
	}

	return existing, nil
}

// Delete removes a route by ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.routes[id]; !ok {
		return ErrRouteNotFound
	}

	delete(s.routes, id)
	return s.saveLocked()
}

// FilePath returns the location of the backing routes.json file.
func (s *Store) FilePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filePath
}

func validateInputs(domain, targetURL string) (string, string, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	targetURL = strings.TrimSpace(targetURL)

	if domain == "" || !domainRegex.MatchString(domain) {
		return "", "", ErrDomainInvalid
	}

	if domain == "reverse.local" {
		return "", "", ErrDomainReserved
	}

	// Auto-prepend http:// if scheme is missing
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "http://" + targetURL
	}

	u, err := url.ParseRequestURI(targetURL)
	if err != nil || u.Host == "" {
		return "", "", ErrTargetURLInvalid
	}

	return domain, targetURL, nil
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
