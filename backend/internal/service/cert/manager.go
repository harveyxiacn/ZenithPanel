package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"path/filepath"
	"time"
)

// Mode enumerates how a cert is produced for a Smart Deploy run.
type Mode string

const (
	ModeReality    Mode = "reality"
	ModeACME       Mode = "acme"
	ModeSelfSigned Mode = "self_signed"
	ModeExisting   Mode = "existing"
)

// ProvisionInput carries everything Manager.Provision needs. Which fields
// actually matter depends on Mode:
//
//	Mode == ModeReality:    nothing — cert is not involved
//	Mode == ModeACME:       Domain + Email
//	Mode == ModeSelfSigned: PublicIP (goes into the SAN) + Domain (optional)
//	Mode == ModeExisting:   CertPath + KeyPath
type ProvisionInput struct {
	Mode     Mode
	Domain   string
	Email    string
	PublicIP string
	CertPath string
	KeyPath  string
}

// ProvisionResult is what a deployment writes into its inbound TLS settings
// (or skips, for Reality).
type ProvisionResult struct {
	Mode       Mode      `json:"mode"`
	CertPath   string    `json:"cert_path,omitempty"`
	KeyPath    string    `json:"key_path,omitempty"`
	NotAfter   time.Time `json:"not_after,omitempty"`
	SelfSigned bool      `json:"self_signed,omitempty"`
}

// ACMEClient is a narrow seam over lego so tests can swap in a stub. The
// production implementation lives in acme_lego.go (added when ACME is wired
// in; for Phase 1 callers may pass a stub that returns ErrACMENotConfigured).
type ACMEClient interface {
	Obtain(ctx context.Context, domain, email string) (certPEM, keyPEM []byte, expires time.Time, err error)
}

// ErrACMENotConfigured is returned by stub ACME clients to signal that the
// user asked for an ACME-issued cert but no ACME client is wired in yet.
var ErrACMENotConfigured = errors.New("ACME client is not configured; provide a domain or use self-signed")

// Manager provisions certificates per the chosen mode and writes results to
// a persistent root directory.
type Manager struct {
	root string
	acme ACMEClient
}

// NewManager returns a Manager rooted at dir (typically
// /etc/zenithpanel/certs). A nil acme client is fine; Mode==ModeACME will
// just return ErrACMENotConfigured.
func NewManager(dir string, acme ACMEClient) *Manager {
	return &Manager{root: dir, acme: acme}
}

// Provision dispatches on Mode and returns a ready-to-use cert path pair.
func (m *Manager) Provision(ctx context.Context, in ProvisionInput) (*ProvisionResult, error) {
	switch in.Mode {
	case ModeReality:
		return &ProvisionResult{Mode: ModeReality}, nil
	case ModeACME:
		return m.provisionACME(ctx, in)
	case ModeSelfSigned:
		return m.provisionSelfSigned(in)
	case ModeExisting:
		return m.provisionExisting(in)
	default:
		return nil, fmt.Errorf("cert: unknown mode %q", in.Mode)
	}
}

func (m *Manager) provisionACME(ctx context.Context, in ProvisionInput) (*ProvisionResult, error) {
	if in.Domain == "" {
		return nil, errors.New("cert: ACME mode requires a domain")
	}
	if !validDomain(in.Domain) {
		return nil, fmt.Errorf("cert: invalid domain %q", in.Domain)
	}
	if m.acme == nil {
		return nil, ErrACMENotConfigured
	}

	certPEM, keyPEM, expires, err := m.acme.Obtain(ctx, in.Domain, in.Email)
	if err != nil {
		return nil, fmt.Errorf("cert: ACME obtain: %w", err)
	}
	certPath, keyPath, err := m.writePair(in.Domain, certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		Mode:     ModeACME,
		CertPath: certPath,
		KeyPath:  keyPath,
		NotAfter: expires,
	}, nil
}

func (m *Manager) provisionSelfSigned(in ProvisionInput) (*ProvisionResult, error) {
	sanName := in.Domain
	if sanName == "" {
		sanName = "self-signed-" + sanitizeForPath(in.PublicIP)
	}
	if sanName == "self-signed-" {
		sanName = "self-signed-localhost"
	}

	certPEM, keyPEM, notAfter, err := generateSelfSignedWithSAN(in.PublicIP, in.Domain)
	if err != nil {
		return nil, fmt.Errorf("cert: self-signed: %w", err)
	}
	certPath, keyPath, err := m.writePair(sanName, certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &ProvisionResult{
		Mode:       ModeSelfSigned,
		CertPath:   certPath,
		KeyPath:    keyPath,
		NotAfter:   notAfter,
		SelfSigned: true,
	}, nil
}

func (m *Manager) provisionExisting(in ProvisionInput) (*ProvisionResult, error) {
	if in.CertPath == "" || in.KeyPath == "" {
		return nil, errors.New("cert: existing mode requires cert_path and key_path")
	}
	notAfter, err := ValidatePair(in.CertPath, in.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("cert: validate existing pair: %w", err)
	}
	return &ProvisionResult{
		Mode:     ModeExisting,
		CertPath: in.CertPath,
		KeyPath:  in.KeyPath,
		NotAfter: notAfter,
	}, nil
}

// writePair saves cert + key PEM content to
// <root>/<tag>/{fullchain.pem,privkey.pem}. File perms mirror Let's
// Encrypt's default layout: cert 0644, key 0600, dir 0700.
func (m *Manager) writePair(tag string, certPEM, keyPEM []byte) (string, string, error) {
	dir := filepath.Join(m.root, sanitizeForPath(tag))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

// ValidatePair loads a cert + key from disk, confirms they form a usable TLS
// pair, and returns the cert's NotAfter. This is the integrity check used by
// ModeExisting and by the renewal ticker.
func ValidatePair(certPath, keyPath string) (time.Time, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return time.Time{}, err
	}
	if len(pair.Certificate) == 0 {
		return time.Time{}, errors.New("cert file contained no certificates")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return time.Time{}, err
	}
	return leaf.NotAfter, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Self-signed generation with SAN
// ─────────────────────────────────────────────────────────────────────────

// generateSelfSignedWithSAN emits a 10-year ECDSA-P256 cert with the given
// IP and/or domain in its SubjectAltNames. An empty publicIP and empty
// domain produces a localhost cert (useful for dev).
func generateSelfSignedWithSAN(publicIP, domain string) ([]byte, []byte, time.Time, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, time.Time{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, time.Time{}, err
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour)

	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "zenithpanel-self-signed"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	addedSAN := false
	if publicIP != "" {
		if ip := net.ParseIP(publicIP); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			addedSAN = true
		}
	}
	if domain != "" {
		tmpl.DNSNames = append(tmpl.DNSNames, domain)
		addedSAN = true
	}
	if !addedSAN {
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	return certPEM, keyPEM, notAfter, nil
}

// rsaSelfSignedForTest generates an RSA self-signed pair that intentionally
// does not match any other key. Used by tests to confirm mismatch rejection.
// Not used by production code.
func rsaSelfSignedForTest() ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(42),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		Subject:      pkix.Name{CommonName: "rsa-test"},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		DNSNames:     []string{"rsa-test.local"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	return certPEM, keyPEM, nil
}

// sanitizeForPath strips characters that would split into subdirectories or
// escape upward. Good enough for tags we control (domain names, IPs,
// self-signed labels). Leading dots are replaced with '_' so the result
// cannot be "." or ".." even if the caller passed such a string.
func sanitizeForPath(s string) string {
	if s == "" {
		return "_"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			out = append(out, c)
		case c == '-' || c == '_':
			out = append(out, c)
		case c == '.':
			// Disallow leading dots to prevent traversal (".." or ".foo").
			if len(out) == 0 {
				out = append(out, '_')
			} else {
				out = append(out, '.')
			}
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

func validDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 {
		return false
	}
	return domainRe.MatchString(d)
}
