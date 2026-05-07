package cert

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// domainRe validates domain names to prevent path traversal via crafted domains.
var domainRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

// certDir is the fixed directory for storing certificates.
const certDir = "/opt/zenithpanel/data/certs"

// acmeUser implements the lego registration.User interface.
type acmeUser struct {
	email        string
	registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

// IssueCertificate obtains a Let's Encrypt certificate for the given domain
// using the HTTP-01 challenge. It requires port 80 to be reachable from the internet.
// On success the cert and key are written to certDir/<domain>.{crt,key} and their
// paths are returned so callers can persist them.
func IssueCertificate(domain string, email string) error {
	certPath, keyPath, err := ObtainCert(domain, email)
	if err != nil {
		return err
	}
	_ = certPath
	_ = keyPath
	return nil
}

// ObtainCert runs the full ACME HTTP-01 flow and returns the on-disk cert/key paths.
func ObtainCert(domain string, email string) (certPath, keyPath string, err error) {
	if !domainRe.MatchString(domain) || len(domain) > 253 {
		return "", "", fmt.Errorf("invalid domain name")
	}
	if email == "" {
		return "", "", fmt.Errorf("email is required for ACME registration")
	}

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate account key: %w", err)
	}

	user := &acmeUser{email: email, key: privKey}

	cfg := lego.NewConfig(user)
	cfg.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(cfg)
	if err != nil {
		return "", "", fmt.Errorf("create ACME client: %w", err)
	}

	// HTTP-01 challenge: lego starts a temporary server on :80 to serve the token.
	// Requires port 80 to be accessible from the internet.
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", "80")); err != nil {
		return "", "", fmt.Errorf("set HTTP-01 provider: %w", err)
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
	if err != nil {
		return "", "", fmt.Errorf("ACME registration: %w", err)
	}
	user.registration = reg

	request := certificate.ObtainRequest{
		Domains: []string{domain},
		Bundle:  true,
	}
	certs, err := client.Certificate.Obtain(request)
	if err != nil {
		return "", "", fmt.Errorf("obtain certificate: %w", err)
	}

	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", "", fmt.Errorf("create cert dir: %w", err)
	}

	base := filepath.Join(certDir, filepath.Base(domain))
	certPath = base + ".crt"
	keyPath = base + ".key"

	if err := os.WriteFile(certPath, certs.Certificate, 0600); err != nil {
		return "", "", fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, certs.PrivateKey, 0600); err != nil {
		return "", "", fmt.Errorf("write key: %w", err)
	}

	return certPath, keyPath, nil
}
