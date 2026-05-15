// Package webserver implements a lightweight virtual-hosting layer that can
// reverse-proxy, serve static files, or issue redirects for custom domains.
// It listens on :80 (HTTP) and :443 (HTTPS) independently from the panel port.
// TLS certificates are loaded per-domain using SNI; ACME certificates are
// obtained using the existing cert.ObtainCert helper.
package webserver

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"gorm.io/gorm"
)

// Manager manages virtual-host HTTP/HTTPS servers.
type Manager struct {
	mu      sync.RWMutex
	db      *gorm.DB
	srv80   *http.Server                // plain HTTP (redirects to HTTPS or serves directly)
	srv443  *http.Server                // HTTPS with SNI
	certs   map[string]*tls.Certificate // domain → certificate
	running bool
}

var instance *Manager
var once sync.Once

// Get returns the singleton Manager. Call Init before Get.
func Get() *Manager { return instance }

// Init creates the global Manager singleton. Must be called once at startup.
func Init(db *gorm.DB) {
	once.Do(func() {
		instance = &Manager{db: db, certs: map[string]*tls.Certificate{}}
	})
}

// Start loads all enabled sites from DB and begins listening.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return nil
	}

	if err := m.reload(); err != nil {
		return err
	}
	m.running = true
	return nil
}

// Reload re-reads the DB and hot-swaps the TLS config and handler without
// dropping existing connections. Safe to call from HTTP handlers.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reload()
}

// Stop gracefully shuts down both servers.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeListenersLocked()
	m.running = false
}

// closeListenersLocked tears down both HTTP servers and nils the pointers so
// the next reload re-creates them from scratch. Caller must hold m.mu. Close
// errors are non-actionable (the listener is usually already gone) so we
// drop them.
func (m *Manager) closeListenersLocked() {
	if m.srv80 != nil {
		_ = m.srv80.Close()
		m.srv80 = nil
	}
	if m.srv443 != nil {
		_ = m.srv443.Close()
		m.srv443 = nil
	}
}

// reload must be called with m.mu held.
func (m *Manager) reload() error {
	var sites []model.Site
	if err := m.db.Where("enable = ?", true).Find(&sites).Error; err != nil {
		return fmt.Errorf("load sites: %w", err)
	}

	// Build a new mux and cert map from the loaded sites.
	mux80 := http.NewServeMux()
	mux443 := http.NewServeMux()
	newCerts := map[string]*tls.Certificate{}

	for _, s := range sites {
		s := s // capture
		handler, err := buildHandler(s)
		if err != nil {
			log.Printf("webserver: skip site %q: %v", s.Name, err)
			continue
		}

		mux80.Handle(s.Domain+"/", handler)
		mux443.Handle(s.Domain+"/", handler)

		// Load/obtain TLS certificate
		if s.TLSMode == "custom" && s.CertPath != "" && s.KeyPath != "" {
			c, err := tls.LoadX509KeyPair(s.CertPath, s.KeyPath)
			if err != nil {
				log.Printf("webserver: site %q custom cert load failed: %v", s.Name, err)
				continue
			}
			newCerts[s.Domain] = &c
		} else if s.TLSMode == "acme" && s.TLSEmail != "" {
			// Try to obtain/reuse via lego HTTP-01 challenge.
			// This blocks briefly; in production use a background goroutine.
			certPath, keyPath, err := cert.ObtainCert(s.Domain, s.TLSEmail)
			if err != nil {
				log.Printf("webserver: site %q ACME failed: %v", s.Name, err)
				continue
			}
			c, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				log.Printf("webserver: site %q ACME cert load failed: %v", s.Name, err)
				continue
			}
			newCerts[s.Domain] = &c
		}
	}
	m.certs = newCerts

	// Lazy bind: with no enabled sites, don't hold :80/:443. Idle binding
	// blocks ACME's HTTP-01 challenge, conflicts with other services, and
	// presents a 404 listener that's pure noise. When the user creates the
	// first Site, this path runs again via the /sites POST handler and
	// brings the listeners up.
	if len(sites) == 0 {
		m.closeListenersLocked()
		return nil
	}

	// HTTP :80 — only when we have at least one site that wants it.
	if m.srv80 != nil {
		m.srv80.Handler = mux80
	} else {
		m.srv80 = &http.Server{
			Addr:         ":80",
			Handler:      mux80,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 60 * time.Second,
		}
		go func() {
			if err := m.srv80.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("webserver: HTTP :80 error: %v", err)
			}
		}()
	}

	// HTTPS :443 — only start if we have at least one certificate
	if len(m.certs) > 0 {
		tlsCfg := &tls.Config{
			GetCertificate: func(hi *tls.ClientHelloInfo) (*tls.Certificate, error) {
				m.mu.RLock()
				c, ok := m.certs[hi.ServerName]
				m.mu.RUnlock()
				if !ok {
					return nil, fmt.Errorf("no cert for %q", hi.ServerName)
				}
				return c, nil
			},
			MinVersion: tls.VersionTLS12,
		}
		if m.srv443 != nil {
			m.srv443.Handler = mux443
		} else {
			m.srv443 = &http.Server{
				Addr:         ":443",
				Handler:      mux443,
				TLSConfig:    tlsCfg,
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 60 * time.Second,
			}
			go func() {
				ln, err := net.Listen("tcp", ":443")
				if err != nil {
					log.Printf("webserver: HTTPS :443 listen failed: %v", err)
					return
				}
				tlsLn := tls.NewListener(ln, tlsCfg)
				if err := m.srv443.Serve(tlsLn); err != nil && err != http.ErrServerClosed {
					log.Printf("webserver: HTTPS :443 error: %v", err)
				}
			}()
		}
	}
	return nil
}

// buildHandler creates the appropriate http.Handler for a site.
func buildHandler(s model.Site) (http.Handler, error) {
	switch s.Type {
	case "reverse_proxy":
		if s.UpstreamURL == "" {
			return nil, fmt.Errorf("upstream_url is required for reverse_proxy")
		}
		target, err := url.Parse(s.UpstreamURL)
		if err != nil {
			return nil, fmt.Errorf("invalid upstream_url: %w", err)
		}
		rp := httputil.NewSingleHostReverseProxy(target)
		// Inject custom headers if configured
		headers := parseHeaders(s.CustomHeaders)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Host = target.Host
			for k, v := range headers {
				r.Header.Set(k, v)
			}
			rp.ServeHTTP(w, r)
		}), nil

	case "static":
		if s.RootPath == "" {
			return nil, fmt.Errorf("root_path is required for static")
		}
		if _, err := os.Stat(s.RootPath); err != nil {
			return nil, fmt.Errorf("root_path does not exist: %w", err)
		}
		return http.FileServer(http.Dir(s.RootPath)), nil

	case "redirect":
		if s.RedirectURL == "" {
			return nil, fmt.Errorf("redirect_url is required for redirect")
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.RedirectURL, http.StatusMovedPermanently)
		}), nil
	}
	return nil, fmt.Errorf("unknown site type %q", s.Type)
}

// parseHeaders decodes the JSON [{key,value}] custom-headers blob.
func parseHeaders(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var pairs []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal([]byte(raw), &pairs); err != nil {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if p.Key != "" {
			out[p.Key] = p.Value
		}
	}
	return out
}
