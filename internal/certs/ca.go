package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	CAName = "ReverseLocal Local CA"
)

// CAManager manages the Root CA and dynamically signs leaf certificates for SNI.
type CAManager struct {
	mu          sync.RWMutex
	certDir     string
	caCert      *x509.Certificate
	caKey       *rsa.PrivateKey
	caCertPEM   []byte
	certCache   map[string]*tls.Certificate
	isInstalled bool
}

// DefaultCertDir returns the default directory for storing the Root CA certificate and key.
func DefaultCertDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "certs"
	}
	return filepath.Join(configDir, "reverse-local", "certs")
}

// NewCAManager loads an existing Root CA or generates a new one in certDir.
func NewCAManager(certDir string) (*CAManager, error) {
	if certDir == "" {
		certDir = DefaultCertDir()
	}

	if err := os.MkdirAll(certDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create cert directory %s: %w", certDir, err)
	}

	mgr := &CAManager{
		certDir:   certDir,
		certCache: make(map[string]*tls.Certificate),
	}

	caCertPath := filepath.Join(certDir, "ca.crt")
	caKeyPath := filepath.Join(certDir, "ca.key")

	if fileExists(caCertPath) && fileExists(caKeyPath) {
		if err := mgr.loadCA(caCertPath, caKeyPath); err != nil {
			// If corrupted, re-generate
			if err := mgr.generateCA(caCertPath, caKeyPath); err != nil {
				return nil, err
			}
		}
	} else {
		if err := mgr.generateCA(caCertPath, caKeyPath); err != nil {
			return nil, err
		}
	}

	mgr.isInstalled = mgr.checkInstalled()
	return mgr, nil
}

// CACertPEM returns the PEM-encoded Root CA certificate for download/export.
func (m *CAManager) CACertPEM() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.caCertPEM
}

// CACertPath returns the path to ca.crt.
func (m *CAManager) CACertPath() string {
	return filepath.Join(m.certDir, "ca.crt")
}

// IsInstalled returns true if the Root CA is currently installed in the OS trust store.
func (m *CAManager) IsInstalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isInstalled
}

// GetCertificate dynamically retrieves or generates a leaf certificate for the requested SNI host.
func (m *CAManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	domain := strings.ToLower(strings.TrimSpace(hello.ServerName))
	if domain == "" {
		domain = "reverse.local"
	}

	// Check cache
	m.mu.RLock()
	if cert, ok := m.certCache[domain]; ok {
		m.mu.RUnlock()
		return cert, nil
	}
	m.mu.RUnlock()

	// Generate and cache leaf cert
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check cache after acquiring write lock
	if cert, ok := m.certCache[domain]; ok {
		return cert, nil
	}

	cert, err := m.generateLeafCertificate(domain)
	if err != nil {
		return nil, fmt.Errorf("failed to generate TLS certificate for %s: %w", domain, err)
	}

	m.certCache[domain] = cert
	return cert, nil
}

// generateLeafCertificate generates an RSA 2048 leaf certificate signed by our Root CA.
func (m *CAManager) generateLeafCertificate(domain string) (*tls.Certificate, error) {
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	dnsNames := []string{domain}
	var ipAddresses []net.IP

	if domain == "reverse.local" {
		dnsNames = append(dnsNames, "localhost")
		ipAddresses = append(ipAddresses, net.ParseIP("127.0.0.1"), net.IPv6loopback)
	}

	if ip := net.ParseIP(domain); ip != nil {
		ipAddresses = append(ipAddresses, ip)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   domain,
			Organization: []string{"ReverseLocal"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(2, 0, 0), // 2 years
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, &template, m.caCert, &leafKey.PublicKey, m.caKey)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{leafDER, m.caCert.Raw},
		PrivateKey:  leafKey,
	}

	return tlsCert, nil
}

// generateCA creates a new self-signed Root CA and writes ca.crt and ca.key.
func (m *CAManager) generateCA(certPath, keyPath string) error {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate CA private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return fmt.Errorf("failed to generate CA serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:         CAName,
			Organization:       []string{"ReverseLocal Development"},
			OrganizationalUnit: []string{"Local Proxy"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years validity
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("failed to create self-signed CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return fmt.Errorf("failed to parse generated CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caDER,
	})

	keyBytes, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	})

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to save ca.crt: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to save ca.key: %w", err)
	}

	m.caCert = caCert
	m.caKey = caKey
	m.caCertPEM = certPEM
	return nil
}

// loadCA reads existing ca.crt and ca.key from disk.
func (m *CAManager) loadCA(certPath, keyPath string) error {
	certData, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}

	certBlock, _ := pem.Decode(certData)
	if certBlock == nil {
		return errors.New("invalid CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	keyBlock, _ := pem.Decode(keyData)
	if keyBlock == nil {
		return errors.New("invalid CA key PEM")
	}

	var caKey *rsa.PrivateKey
	if parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err == nil {
		if rsaKey, ok := parsedKey.(*rsa.PrivateKey); ok {
			caKey = rsaKey
		}
	} else if rsaKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes); err == nil {
		caKey = rsaKey
	}

	if caKey == nil {
		return errors.New("failed to parse RSA private key from CA key file")
	}

	m.caCert = caCert
	m.caKey = caKey
	m.caCertPEM = certData
	return nil
}

// InstallRootCA attempts to install the Root CA into the operating system's trust store.
func (m *CAManager) InstallRootCA() error {
	caPath := m.CACertPath()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// certutil -addstore -f "Root" <caPath>
		cmd = exec.Command("certutil", "-addstore", "-f", "Root", caPath)
	case "darwin":
		// security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain <caPath>
		cmd = exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", caPath)
	case "linux":
		// Check for Debian/Ubuntu update-ca-certificates
		if _, err := exec.LookPath("update-ca-certificates"); err == nil {
			dest := "/usr/local/share/ca-certificates/reverse-local-ca.crt"
			if err := copyFile(caPath, dest); err != nil {
				return fmt.Errorf("failed to copy CA to %s (root privileges required): %w", dest, err)
			}
			cmd = exec.Command("update-ca-certificates")
		} else if _, err := exec.LookPath("update-ca-trust"); err == nil {
			// Fedora / RHEL
			dest := "/etc/pki/ca-trust/source/anchors/reverse-local-ca.crt"
			if err := copyFile(caPath, dest); err != nil {
				return fmt.Errorf("failed to copy CA to %s (root privileges required): %w", dest, err)
			}
			cmd = exec.Command("update-ca-trust", "extract")
		} else if _, err := exec.LookPath("trust"); err == nil {
			// Arch Linux
			dest := "/etc/ca-certificates/trust-source/anchors/reverse-local-ca.crt"
			if err := copyFile(caPath, dest); err != nil {
				return fmt.Errorf("failed to copy CA to %s (root privileges required): %w", dest, err)
			}
			cmd = exec.Command("trust", "extract-compat")
		} else {
			return errors.New("no supported CA trust utility found (expected update-ca-certificates or update-ca-trust)")
		}
	default:
		return fmt.Errorf("unsupported operating system for automatic CA installation: %s", runtime.GOOS)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to install Root CA into system trust store: %s: %w", string(output), err)
	}

	m.mu.Lock()
	m.isInstalled = m.checkInstalled()
	m.mu.Unlock()

	return nil
}

// checkInstalled checks if our Root CA is registered in the OS trust store.
func (m *CAManager) checkInstalled() bool {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("certutil", "-verifystore", "Root", CAName)
		err := cmd.Run()
		return err == nil
	case "darwin":
		cmd := exec.Command("security", "find-certificate", "-c", CAName, "/Library/Keychains/System.keychain")
		err := cmd.Run()
		return err == nil
	case "linux":
		// Check common trust directories
		paths := []string{
			"/usr/local/share/ca-certificates/reverse-local-ca.crt",
			"/etc/pki/ca-trust/source/anchors/reverse-local-ca.crt",
			"/etc/ca-certificates/trust-source/anchors/reverse-local-ca.crt",
		}
		for _, p := range paths {
			if fileExists(p) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
