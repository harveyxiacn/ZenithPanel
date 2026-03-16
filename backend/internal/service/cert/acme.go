package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// GenerateSelfSignedCert generates a dummy self-signed certificate for local testing
// In full production, this module would integrate Lego / ACME to issue Let's Encrypt certs
func GenerateSelfSignedCert(certPath, keyPath string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certOut.Close()
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes})

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	privBytes, _ := x509.MarshalPKCS8PrivateKey(priv)
	pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return nil
}

// domainRe validates domain names to prevent path traversal via crafted domains.
var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

// certDir is the fixed directory for storing certificates.
const certDir = "/opt/zenithpanel/data/certs"

// IssueCertificate triggers Lego ACME flow for a specific domain
func IssueCertificate(domain string, email string) error {
	// Validate domain to prevent path traversal (e.g. "../../etc/cron.d/backdoor")
	if !domainRe.MatchString(domain) || len(domain) > 253 {
		return fmt.Errorf("invalid domain name")
	}

	// Store certs in a fixed directory using only the base name
	os.MkdirAll(certDir, 0700)
	certPath := filepath.Join(certDir, filepath.Base(domain)+".crt")
	keyPath := filepath.Join(certDir, filepath.Base(domain)+".key")

	// TODO: Integrate github.com/go-acme/lego/v4
	// For now just simulate creating the cert files
	return GenerateSelfSignedCert(certPath, keyPath)
}
