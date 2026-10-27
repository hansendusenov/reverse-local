package certs

import (
	"crypto/tls"
	"crypto/x509"
	"path/filepath"
	"testing"
)

func TestCAManager(t *testing.T) {
	tempDir := t.TempDir()
	certDir := filepath.Join(tempDir, "certs")

	mgr, err := NewCAManager(certDir)
	if err != nil {
		t.Fatalf("failed to initialize CAManager: %v", err)
	}

	if len(mgr.CACertPEM()) == 0 {
		t.Errorf("expected non-empty CA cert PEM")
	}

	// Test Dynamic Leaf Certificate Generation for "reverse.local"
	helloReverse := &tls.ClientHelloInfo{
		ServerName: "reverse.local",
	}
	cert1, err := mgr.GetCertificate(helloReverse)
	if err != nil {
		t.Fatalf("failed to get certificate for reverse.local: %v", err)
	}
	if len(cert1.Certificate) < 2 {
		t.Errorf("expected at least 2 certificates in chain (leaf + CA), got %d", len(cert1.Certificate))
	}

	leaf1, err := x509.ParseCertificate(cert1.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf certificate: %v", err)
	}
	if leaf1.Subject.CommonName != "reverse.local" {
		t.Errorf("expected CN=reverse.local, got %s", leaf1.Subject.CommonName)
	}

	// Verify caching returns same instance
	cert1Cached, err := mgr.GetCertificate(helloReverse)
	if err != nil {
		t.Fatalf("failed to get cached certificate: %v", err)
	}
	if cert1 != cert1Cached {
		t.Errorf("expected identical pointer from certificate cache")
	}

	// Test Dynamic Leaf Certificate for custom domain "project.local"
	helloProject := &tls.ClientHelloInfo{
		ServerName: "project.local",
	}
	cert2, err := mgr.GetCertificate(helloProject)
	if err != nil {
		t.Fatalf("failed to get certificate for project.local: %v", err)
	}

	leaf2, err := x509.ParseCertificate(cert2.Certificate[0])
	if err != nil {
		t.Fatalf("failed to parse leaf certificate: %v", err)
	}
	if leaf2.Subject.CommonName != "project.local" {
		t.Errorf("expected CN=project.local, got %s", leaf2.Subject.CommonName)
	}

	// Verify CA reloads properly without generating new keys
	mgrReloaded, err := NewCAManager(certDir)
	if err != nil {
		t.Fatalf("failed to reload CAManager: %v", err)
	}
	if string(mgrReloaded.CACertPEM()) != string(mgr.CACertPEM()) {
		t.Errorf("reloaded CA cert PEM does not match original")
	}
}
