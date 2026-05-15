package cert

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RenewalThreshold is how close to expiry we'll trigger a renewal. Let's
// Encrypt certs are valid for 90 days; renewing at <=30 left mirrors the
// official recommendation and gives plenty of headroom for transient
// failures.
const RenewalThreshold = 30 * 24 * time.Hour

// StartRenewer runs a background ticker that scans certDir every 12 hours
// for certs nearing expiry and re-issues them using the stored email.
// Returns a cancel function so graceful shutdown can stop the loop.
//
// Renewal is best-effort: a failure logs and moves on so a single bad cert
// doesn't take the whole loop down. The next tick will retry. Designed to
// run alongside the existing audit / monitor goroutines in main.go.
func StartRenewer(getSetting func(string) string) (cancel func()) {
	stop := make(chan struct{})
	go func() {
		// One immediate scan on boot so a long-offline panel renews soon
		// after start rather than waiting 12h.
		runRenewalScan(getSetting)
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runRenewalScan(getSetting)
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

// runRenewalScan walks certDir, finds <domain>.crt files, checks expiry, and
// renews via ObtainCert if any cert is within RenewalThreshold of expiring.
// Exported only as a package-level function so tests can drive the scan
// directly without spawning the goroutine.
func runRenewalScan(getSetting func(string) string) {
	email := strings.TrimSpace(getSetting("acme_email"))
	if email == "" {
		// No email configured = no ACME account = nothing to renew. Skip
		// silently rather than spamming logs every 12h.
		return
	}
	entries, err := os.ReadDir(certDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("cert renewer: read certDir: %v", err)
		}
		return
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".crt") {
			continue
		}
		domain := strings.TrimSuffix(name, ".crt")
		// Self-signed test certs (fullchain.pem, etc.) don't follow the
		// <domain>.crt pattern; skip anything that doesn't look like a
		// valid domain to avoid trying to ACME-renew them.
		if !domainRe.MatchString(domain) {
			continue
		}
		certPath := filepath.Join(certDir, name)
		keyPath := filepath.Join(certDir, domain+".key")
		notAfter, verr := ValidatePair(certPath, keyPath)
		if verr != nil {
			log.Printf("cert renewer: validate %s: %v (skipping)", domain, verr)
			continue
		}
		if notAfter.Sub(now) > RenewalThreshold {
			continue
		}
		log.Printf("cert renewer: %s expires in %s, renewing…", domain, notAfter.Sub(now).Round(time.Hour))
		if _, _, err := ObtainCert(domain, email); err != nil {
			log.Printf("cert renewer: renew %s failed: %v (will retry on next tick)", domain, err)
			continue
		}
		log.Printf("cert renewer: renewed %s", domain)
	}
}
