package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

var trustedProxyState = struct {
	sync.RWMutex
	networks []*net.IPNet
}{}

// ConfigureTrustedProxies makes proxy-header trust explicit. An empty value
// disables forwarded-IP parsing; a comma-separated value accepts only the
// listed IPs/CIDRs. Gin's unsafe trust-everything default is never used.
func ConfigureTrustedProxies(r *gin.Engine, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if err := r.SetTrustedProxies(nil); err != nil {
			return err
		}
		setTrustedProxyNetworks(nil)
		return nil
	}
	entries := make([]string, 0, 4)
	networks := make([]*net.IPNet, 0, 4)
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err != nil {
				return fmt.Errorf("invalid trusted proxy CIDR %q: %w", entry, err)
			}
			networks = append(networks, network)
		} else {
			ip := net.ParseIP(entry)
			if ip == nil {
				return fmt.Errorf("invalid trusted proxy IP %q", entry)
			}
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		if err := r.SetTrustedProxies(nil); err != nil {
			return err
		}
		setTrustedProxyNetworks(nil)
		return nil
	}
	if err := r.SetTrustedProxies(entries); err != nil {
		return err
	}
	setTrustedProxyNetworks(networks)
	return nil
}

func setTrustedProxyNetworks(networks []*net.IPNet) {
	trustedProxyState.Lock()
	trustedProxyState.networks = append([]*net.IPNet(nil), networks...)
	trustedProxyState.Unlock()
}

func trustedProxyPeer(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = strings.Trim(remoteAddr, "[]")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	trustedProxyState.RLock()
	defer trustedProxyState.RUnlock()
	for _, network := range trustedProxyState.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		c.Header("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' ws: wss:")
		if requestUsesHTTPS(c) {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func panelCORS() gin.HandlerFunc {
	return cors.New(cors.Config{
		AllowOriginFunc: isLoopbackOrigin,
		AllowMethods: []string{
			http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodOptions,
		},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}
