package traffic

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// rdnsEntry is one cached IP→name mapping. Learned entries (sniffed
// domain+IP pairs from a domain-capable tier) take precedence over PTR
// results and are never overwritten by the worker.
type rdnsEntry struct {
	name    string
	learned bool
}

// rdnsResolver maps destination IPs to display names for the IP-only egress
// tiers (socket sampler). Two sources, in priority order:
//
//  1. learn(): sniffed (domain, IP) pairs observed by the Clash tier — the
//     panel's own sing-box sees the SNI for the same upstream IPs the other
//     proxies reach, so this is the most truthful mapping available.
//  2. Reverse DNS (PTR): async cached lookups, same non-blocking pattern as
//     asnResolver — first sight returns "", recurring destinations get the
//     name on a later flush. Negative results are cached as "" so IPs
//     without a PTR record aren't re-queried every flush.
type rdnsResolver struct {
	mu      sync.RWMutex
	cache   map[string]rdnsEntry
	queue   chan string
	res     *net.Resolver
	enabled bool
}

const rdnsCacheCap = 50000

func newRDNSResolver(enabled bool) *rdnsResolver {
	r := &rdnsResolver{
		cache:   make(map[string]rdnsEntry, 1024),
		queue:   make(chan string, 4096),
		enabled: enabled,
		res:     &net.Resolver{},
	}
	if enabled {
		go r.worker()
	}
	return r
}

// learn records a sniffed domain for ip, overriding any PTR result. Always
// active (even with PTR lookups disabled) — it costs nothing and only uses
// data another tier already observed.
func (r *rdnsResolver) learn(ip, host string) {
	if r == nil || ip == "" || host == "" || len(host) > 255 {
		return
	}
	// A "host" that parses as an IP teaches us nothing.
	if net.ParseIP(host) != nil {
		return
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	r.mu.Lock()
	if len(r.cache) >= rdnsCacheCap {
		// Same crude bound as asnResolver: drop-all beats tracking LRU; the
		// working set re-warms in seconds on a proxy box.
		r.cache = make(map[string]rdnsEntry, 1024)
	}
	r.cache[ip] = rdnsEntry{name: host, learned: true}
	r.mu.Unlock()
}

// lookup returns the best-known name for ip ("" when unknown), enqueueing a
// background PTR resolve on a cache miss. Never blocks on DNS.
func (r *rdnsResolver) lookup(ip string) string {
	if r == nil || ip == "" || isPrivateIP(ip) {
		return ""
	}
	r.mu.RLock()
	v, ok := r.cache[ip]
	r.mu.RUnlock()
	if ok {
		return v.name
	}
	if !r.enabled {
		return ""
	}
	select {
	case r.queue <- ip:
	default:
	}
	return ""
}

func (r *rdnsResolver) worker() {
	for ip := range r.queue {
		r.mu.RLock()
		_, ok := r.cache[ip]
		r.mu.RUnlock()
		if ok {
			continue
		}
		name := r.fetch(ip)
		r.mu.Lock()
		if len(r.cache) >= rdnsCacheCap {
			r.cache = make(map[string]rdnsEntry, 1024)
		}
		// Don't clobber a learned mapping that raced in while we resolved.
		if cur, exists := r.cache[ip]; !exists || !cur.learned {
			r.cache[ip] = rdnsEntry{name: name}
		}
		r.mu.Unlock()
	}
}

// fetch performs one PTR lookup for ip and returns the first name, or "".
func (r *rdnsResolver) fetch(ip string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	names, err := r.res.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	name := strings.ToLower(strings.TrimSuffix(names[0], "."))
	if len(name) > 255 {
		return ""
	}
	return name
}
