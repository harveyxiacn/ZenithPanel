package cert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProvisionRealityReturnsEmptyPaths(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	res, err := m.Provision(context.Background(), ProvisionInput{Mode: ModeReality})
	if err != nil {
		t.Fatalf("Provision reality: %v", err)
	}
	if res.Mode != ModeReality {
		t.Errorf("Mode = %q, want reality", res.Mode)
	}
	if res.CertPath != "" || res.KeyPath != "" {
		t.Errorf("reality should not produce cert paths, got cert=%q key=%q", res.CertPath, res.KeyPath)
	}
}

func TestProvisionSelfSignedProducesUsableTLSPair(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	res, err := m.Provision(context.Background(), ProvisionInput{
		Mode:     ModeSelfSigned,
		PublicIP: "203.0.113.42",
	})
	if err != nil {
		t.Fatalf("Provision self_signed: %v", err)
	}

	if !res.SelfSigned {
		t.Errorf("SelfSigned = false, want true")
	}

	// Files exist and load as a valid TLS pair.
	pair, err := tls.LoadX509KeyPair(res.CertPath, res.KeyPath)
	if err != nil {
		t.Fatalf("cert files don't form a valid TLS pair: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	// Public IP must be in the SAN.
	foundIP := false
	for _, ip := range leaf.IPAddresses {
		if ip.String() == "203.0.113.42" {
			foundIP = true
		}
	}
	if !foundIP {
		t.Errorf("public IP not in SAN, got IPs=%v DNSNames=%v", leaf.IPAddresses, leaf.DNSNames)
	}

	// 10-year validity.
	if time.Until(leaf.NotAfter) < 9*365*24*time.Hour {
		t.Errorf("self-signed cert should be valid for ~10y, got NotAfter=%s", leaf.NotAfter)
	}
}

func TestProvisionSelfSignedWithoutPublicIPFallsBackToLocalhost(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	res, err := m.Provision(context.Background(), ProvisionInput{Mode: ModeSelfSigned})
	if err != nil {
		t.Fatalf("Provision self_signed: %v", err)
	}
	pair, _ := tls.LoadX509KeyPair(res.CertPath, res.KeyPath)
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	foundLocalhost := false
	for _, name := range leaf.DNSNames {
		if name == "localhost" {
			foundLocalhost = true
		}
	}
	if !foundLocalhost {
		t.Errorf("expected localhost fallback SAN, got DNSNames=%v", leaf.DNSNames)
	}
}

func TestProvisionExistingValidatesPair(t *testing.T) {
	dir := t.TempDir()
	certPEM, keyPEM, _, err := generateSelfSignedWithSAN("198.51.100.7", "")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	m := NewManager(t.TempDir(), nil)
	res, err := m.Provision(context.Background(), ProvisionInput{
		Mode:     ModeExisting,
		CertPath: certPath,
		KeyPath:  keyPath,
	})
	if err != nil {
		t.Fatalf("Provision existing: %v", err)
	}
	if res.CertPath != certPath || res.KeyPath != keyPath {
		t.Errorf("paths = %q/%q, want %q/%q", res.CertPath, res.KeyPath, certPath, keyPath)
	}
	if res.NotAfter.IsZero() {
		t.Errorf("NotAfter not populated")
	}
}

func TestProvisionExistingRejectsMismatchedPair(t *testing.T) {
	dir := t.TempDir()

	// Cert A (ECDSA) with key from B (RSA): deliberately mismatched.
	certPEM, _, _, err := generateSelfSignedWithSAN("", "a.example.com")
	if err != nil {
		t.Fatalf("gen A: %v", err)
	}
	_, rsaKeyPEM, err := rsaSelfSignedForTest()
	if err != nil {
		t.Fatalf("gen RSA: %v", err)
	}

	certPath := filepath.Join(dir, "a.crt")
	keyPath := filepath.Join(dir, "b.key")
	_ = os.WriteFile(certPath, certPEM, 0o644)
	_ = os.WriteFile(keyPath, rsaKeyPEM, 0o600)

	m := NewManager(t.TempDir(), nil)
	_, err = m.Provision(context.Background(), ProvisionInput{
		Mode:     ModeExisting,
		CertPath: certPath,
		KeyPath:  keyPath,
	})
	if err == nil {
		t.Fatalf("expected mismatch error, got nil")
	}
}

func TestProvisionExistingMissingPaths(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	_, err := m.Provision(context.Background(), ProvisionInput{Mode: ModeExisting})
	if err == nil {
		t.Fatalf("expected error when cert_path/key_path missing")
	}
}

// stubACME is an ACMEClient that returns a prepared cert/key pair. Tests use
// this to drive the ACME code path without talking to Let's Encrypt.
type stubACME struct {
	cert []byte
	key  []byte
	exp  time.Time
	err  error
}

func (s *stubACME) Obtain(_ context.Context, _, _ string) ([]byte, []byte, time.Time, error) {
	return s.cert, s.key, s.exp, s.err
}

func TestProvisionACMEWritesPEMsToRoot(t *testing.T) {
	certPEM, keyPEM, notAfter, err := generateSelfSignedWithSAN("", "svc.example.com")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}

	root := t.TempDir()
	m := NewManager(root, &stubACME{cert: certPEM, key: keyPEM, exp: notAfter})

	res, err := m.Provision(context.Background(), ProvisionInput{
		Mode:   ModeACME,
		Domain: "svc.example.com",
		Email:  "me@example.com",
	})
	if err != nil {
		t.Fatalf("Provision ACME: %v", err)
	}
	if res.Mode != ModeACME {
		t.Errorf("Mode = %q", res.Mode)
	}
	if _, err := os.Stat(res.CertPath); err != nil {
		t.Errorf("expected cert written, got %v", err)
	}
	// Written under <root>/<domain>/
	if dir := filepath.Dir(res.CertPath); filepath.Base(dir) != "svc.example.com" {
		t.Errorf("cert dir = %q, want base = svc.example.com", dir)
	}
}

func TestProvisionACMEWithoutClientReturnsNotConfigured(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	_, err := m.Provision(context.Background(), ProvisionInput{
		Mode:   ModeACME,
		Domain: "svc.example.com",
	})
	if !errors.Is(err, ErrACMENotConfigured) {
		t.Errorf("expected ErrACMENotConfigured, got %v", err)
	}
}

func TestProvisionACMERejectsInvalidDomain(t *testing.T) {
	m := NewManager(t.TempDir(), &stubACME{})
	_, err := m.Provision(context.Background(), ProvisionInput{
		Mode:   ModeACME,
		Domain: "../../etc/passwd",
	})
	if err == nil {
		t.Errorf("expected invalid-domain error")
	}
}

func TestValidatePairRoundtrip(t *testing.T) {
	certPEM, keyPEM, notAfter, err := generateSelfSignedWithSAN("", "x.example.com")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	_ = os.WriteFile(certPath, certPEM, 0o644)
	_ = os.WriteFile(keyPath, keyPEM, 0o600)

	got, err := ValidatePair(certPath, keyPath)
	if err != nil {
		t.Fatalf("ValidatePair: %v", err)
	}
	if got.Sub(notAfter) > time.Second || notAfter.Sub(got) > time.Second {
		t.Errorf("NotAfter drift: got %s, want ~%s", got, notAfter)
	}
}

func TestSanitizeForPathStripsTraversal(t *testing.T) {
	// The exact output isn't load-bearing; only that it cannot be "." or ".."
	// and contains no path separators.
	for _, dangerous := range []string{"..", ".", "../foo", "foo/../bar", "/etc/passwd", ""} {
		got := sanitizeForPath(dangerous)
		if got == "." || got == ".." {
			t.Errorf("sanitizeForPath(%q) = %q, produces a traversal component", dangerous, got)
		}
		if got == "" {
			t.Errorf("sanitizeForPath(%q) = empty string", dangerous)
		}
		for i := 0; i < len(got); i++ {
			if got[i] == '/' || got[i] == '\\' {
				t.Errorf("sanitizeForPath(%q) = %q, contains path separator", dangerous, got)
			}
		}
	}
}

// Smoke test: PEM decoding of generated self-signed certs produces exactly
// one CERTIFICATE block and one PRIVATE KEY block.
func TestGeneratedPEMShape(t *testing.T) {
	certPEM, keyPEM, _, err := generateSelfSignedWithSAN("1.2.3.4", "")
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Errorf("cert PEM not decodable, got block=%+v", block)
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Errorf("key PEM not decodable, got block=%+v", block)
	}
}
