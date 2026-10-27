package cert

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

// NewProductionManager shares the regular panel issuer and certificate renewer.
func NewProductionManager(root string) *Manager { return NewManager(root, panelACME{}) }

type panelACME struct{}

func (panelACME) Obtain(ctx context.Context, domain, email string) ([]byte, []byte, time.Time, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, time.Time{}, err
	}
	certPath, keyPath, err := ObtainCert(domain, email)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, time.Time{}, fmt.Errorf("issuer returned an invalid certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if err := config.SetSetting("acme_email", email); err != nil {
		return nil, nil, time.Time{}, err
	}
	return certPEM, keyPEM, leaf.NotAfter, nil
}
