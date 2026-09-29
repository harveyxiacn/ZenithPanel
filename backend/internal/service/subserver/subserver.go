// Package subserver runs an optional public listener that serves only
// subscription documents, so client apps can refresh their subscription
// while the admin panel port stays closed to the internet (reachable only
// via SSH tunnel / whitelist).
//
// URL shape: <scheme>://<host>:<port>/<secret>/<client-uuid>[?format=clash]
// The random secret path keeps the endpoint from being enumerated; the
// client UUID is itself unguessable. TLS is used whenever the panel has a
// valid certificate (tls_cert_path / tls_key_path) — subscriptions contain
// node credentials, so plain HTTP is allowed but flagged in the UI.
package subserver

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
)

// Settings keys.
const (
	SettingEnabled    = "sub_server_enabled"
	SettingPort       = "sub_server_port"
	SettingSecret     = "sub_server_secret"
	SettingPublicHost = "sub_server_public_host" // optional override for the URL host
	SettingCertDomain = "sub_server_cert_domain" // optional ACME cert (data/certs/<domain>.crt) for TLS
	DefaultPort       = 2096
)

// Config is the persisted configuration.
type Config struct {
	Enabled    bool   `json:"enabled"`
	Port       int    `json:"port"`
	Secret     string `json:"secret"`
	PublicHost string `json:"public_host"`
	// CertDomain selects an ACME certificate issued by the panel for this
	// listener only, so the admin panel can stay on plain HTTP behind its
	// SSH tunnel. Empty = use the panel certificate if one is configured.
	CertDomain string `json:"cert_domain"`
}

// Status is Config plus runtime facts for the UI.
type Status struct {
	Config
	Running bool   `json:"running"`
	TLS     bool   `json:"tls"`
	BaseURL string `json:"base_url,omitempty"` // e.g. https://host:2096/<secret>/
	Error   string `json:"error,omitempty"`
}

// Load reads the configuration from settings, filling defaults.
func Load() Config {
	cfg := Config{
		Enabled:    config.GetSetting(SettingEnabled) == "true",
		Port:       DefaultPort,
		Secret:     config.GetSetting(SettingSecret),
		PublicHost: strings.TrimSpace(config.GetSetting(SettingPublicHost)),
		CertDomain: strings.TrimSpace(config.GetSetting(SettingCertDomain)),
	}
	if p, err := strconv.Atoi(config.GetSetting(SettingPort)); err == nil && p > 0 && p < 65536 {
		cfg.Port = p
	}
	return cfg
}

// Save persists cfg.
func Save(cfg Config) error {
	for k, v := range map[string]string{
		SettingEnabled:    strconv.FormatBool(cfg.Enabled),
		SettingPort:       strconv.Itoa(cfg.Port),
		SettingSecret:     cfg.Secret,
		SettingPublicHost: cfg.PublicHost,
		SettingCertDomain: cfg.CertDomain,
	} {
		if err := config.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

// NewSecret returns a random URL-safe path segment.
func NewSecret() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return string(b)
}

// Server owns the listener.
type Server struct {
	handler gin.HandlerFunc // serves one subscription; reads c.Param("uuid")

	mu      sync.Mutex
	srv     *http.Server
	cfg     Config
	tls     bool
	certCN  string
	lastErr string
}

// New creates a server that answers with handler (typically a rate-limited
// wrapper around sub.GenerateSubscription).
func New(handler gin.HandlerFunc) *Server { return &Server{handler: handler} }

// Apply (re)starts the listener to match cfg; a disabled cfg stops it.
func (s *Server) Apply(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	s.cfg, s.lastErr, s.tls, s.certCN = cfg, "", false, ""
	if !cfg.Enabled {
		return nil
	}
	if cfg.Secret == "" {
		s.lastErr = "missing secret"
		return errors.New(s.lastErr)
	}

	r := gin.New()
	_ = r.SetTrustedProxies(nil) // ClientIP (rate limiting) must not honour X-Forwarded-For
	r.Use(gin.Recovery())
	r.GET("/"+cfg.Secret+"/:uuid", s.handler)
	r.NoRoute(func(c *gin.Context) { c.Status(http.StatusNotFound) })

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.Port))
	if err != nil {
		s.lastErr = err.Error()
		return fmt.Errorf("subscription server: %w", err)
	}
	srv := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}

	if cfg.CertDomain != "" {
		certPath, keyPath, ok := cert.ACMEPaths(cfg.CertDomain)
		if !ok {
			_ = ln.Close()
			s.lastErr = "invalid certificate domain"
			return errors.New(s.lastErr)
		}
		loader := &fileCert{certPath: certPath, keyPath: keyPath}
		if _, err := loader.get(nil); err != nil {
			_ = ln.Close()
			s.lastErr = "certificate for " + cfg.CertDomain + ": " + err.Error()
			return errors.New(s.lastErr)
		}
		srv.TLSConfig = &tls.Config{GetCertificate: loader.get, MinVersion: tls.VersionTLS12}
		ln = tls.NewListener(ln, srv.TLSConfig)
		s.tls, s.certCN = true, cfg.CertDomain
	} else if c, cn, ok := panelCert(); ok {
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{c}, MinVersion: tls.VersionTLS12}
		ln = tls.NewListener(ln, srv.TLSConfig)
		s.tls, s.certCN = true, cn
	}
	s.srv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("subscription server: %v", err)
			s.mu.Lock()
			s.lastErr = err.Error()
			s.mu.Unlock()
		}
	}()
	scheme := "http"
	if s.tls {
		scheme = "https"
	}
	log.Printf("Subscription server listening on %s://0.0.0.0:%d/<secret>/", scheme, cfg.Port)
	return nil
}

func (s *Server) stopLocked() {
	if s.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.srv.Shutdown(ctx)
	s.srv = nil
}

// Stop shuts the listener down.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

// Status reports the configuration and runtime state. fallbackHost is used
// for the URL when no public host is configured and no certificate names one
// (callers pass the server's public IP or the panel request host).
func (s *Server) Status(fallbackHost string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Config: s.cfg, Running: s.srv != nil, TLS: s.tls, Error: s.lastErr}
	if !st.Running {
		return st
	}
	host := s.cfg.PublicHost
	if host == "" && s.tls {
		host = s.certCN // must match the certificate for clients to verify it
	}
	if host == "" {
		host = fallbackHost
	}
	scheme := "http"
	if s.tls {
		scheme = "https"
	}
	st.BaseURL = fmt.Sprintf("%s://%s/%s/", scheme, net.JoinHostPort(host, strconv.Itoa(s.cfg.Port)), s.cfg.Secret)
	return st
}

// panelCert loads the panel's TLS certificate (if configured and valid) and
// returns the first DNS name it covers.
func panelCert() (tls.Certificate, string, bool) {
	certPath, keyPath := config.GetSetting("tls_cert_path"), config.GetSetting("tls_key_path")
	if certPath == "" || keyPath == "" {
		return tls.Certificate{}, "", false
	}
	certPEM, err1 := os.ReadFile(certPath)
	keyPEM, err2 := os.ReadFile(keyPath)
	if err1 != nil || err2 != nil {
		return tls.Certificate{}, "", false
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", false
	}
	return cert, certName(certPEM), true
}

func certName(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	for _, n := range c.DNSNames {
		if !strings.HasPrefix(n, "*.") {
			return n
		}
	}
	return c.Subject.CommonName
}

// fileCert serves a certificate from disk, reloading it when the file
// changes — the ACME renewer rewrites the same paths, so renewals take
// effect without restarting the listener.
type fileCert struct {
	certPath, keyPath string

	mu    sync.Mutex
	mtime time.Time
	cert  *tls.Certificate
}

func (f *fileCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	st, err := os.Stat(f.certPath)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cert != nil && st.ModTime().Equal(f.mtime) {
		return f.cert, nil
	}
	c, err := tls.LoadX509KeyPair(f.certPath, f.keyPath)
	if err != nil {
		if f.cert != nil {
			return f.cert, nil // keep serving the previous cert mid-renewal
		}
		return nil, err
	}
	f.cert, f.mtime = &c, st.ModTime()
	return f.cert, nil
}
