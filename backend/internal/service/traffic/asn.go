package traffic

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// asnInfo is the resolved autonomous-system metadata for a destination IP.
type asnInfo struct {
	asn     string // e.g. "AS13335"
	org     string // e.g. "CLOUDFLARENET, US"
	country string // ISO-3166 alpha-2, e.g. "US"
}

// asnResolver maps destination IPs to ASN/org/country using Team Cymru's free
// DNS interface (origin.asn.cymru.com). It is deliberately dependency-free: no
// MaxMind DB, no account, no bundled file — just cached DNS TXT lookups.
//
// Lookups are async: lookup() is non-blocking and returns whatever is cached
// (empty on first sight), enqueueing unknown IPs for a background worker. The
// collector writes the empty ASN on a destination's first bucket and the
// enriched value on subsequent buckets — proxy destinations recur constantly,
// so the lag is invisible in practice and the 30s flush is never blocked on DNS.
type asnResolver struct {
	mu      sync.RWMutex
	cache   map[string]asnInfo
	queue   chan string
	res     *net.Resolver
	enabled bool
}

const asnCacheCap = 50000

func newASNResolver(enabled bool) *asnResolver {
	r := &asnResolver{
		cache:   make(map[string]asnInfo, 1024),
		queue:   make(chan string, 4096),
		enabled: enabled,
		res:     &net.Resolver{},
	}
	if enabled {
		go r.worker()
	}
	return r
}

// lookup returns cached ASN info for ip, enqueuing a background resolve on a
// cache miss. Never blocks on DNS. Private/loopback/unspecified IPs resolve to
// empty without a lookup.
func (r *asnResolver) lookup(ip string) asnInfo {
	if r == nil || !r.enabled || ip == "" || isPrivateIP(ip) {
		return asnInfo{}
	}
	r.mu.RLock()
	v, ok := r.cache[ip]
	r.mu.RUnlock()
	if ok {
		return v
	}
	// Enqueue for the worker; drop silently if the queue is saturated (it will
	// be retried next time the destination is observed).
	select {
	case r.queue <- ip:
	default:
	}
	return asnInfo{}
}

func (r *asnResolver) worker() {
	for ip := range r.queue {
		// Skip if another observation already resolved it.
		r.mu.RLock()
		_, ok := r.cache[ip]
		r.mu.RUnlock()
		if ok {
			continue
		}
		info := r.fetch(ip)
		r.mu.Lock()
		if len(r.cache) >= asnCacheCap {
			// Crude bound: drop the whole cache rather than track LRU. On a
			// proxy box the working set re-warms in seconds and this keeps
			// memory flat without a dependency.
			r.cache = make(map[string]asnInfo, 1024)
		}
		r.cache[ip] = info
		r.mu.Unlock()
	}
}

// fetch performs the Team Cymru DNS lookups for one IPv4 address. IPv6 is not
// resolved (returns empty) — proxy egress on these hosts is overwhelmingly v4
// and v6 would need the origin6 zone with nibble-reversed names.
func (r *asnResolver) fetch(ip string) asnInfo {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return asnInfo{}
	}
	rev := reverseIPv4(parsed.To4())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	// origin lookup: "ASN | prefix | country | registry | date"
	txts, err := r.res.LookupTXT(ctx, rev+".origin.asn.cymru.com")
	if err != nil || len(txts) == 0 {
		return asnInfo{}
	}
	fields := splitCymru(txts[0])
	if len(fields) < 3 {
		return asnInfo{}
	}
	asNum := strings.Fields(fields[0]) // first ASN if multiple are listed
	info := asnInfo{country: fields[2]}
	if len(asNum) > 0 && asNum[0] != "" {
		info.asn = "AS" + asNum[0]
		// org lookup: "ASN | country | registry | date | ORG-NAME"
		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()
		if otxts, oerr := r.res.LookupTXT(ctx2, "AS"+asNum[0]+".asn.cymru.com"); oerr == nil && len(otxts) > 0 {
			of := splitCymru(otxts[0])
			if len(of) > 0 {
				info.org = of[len(of)-1]
			}
		}
	}
	return info
}

func splitCymru(s string) []string {
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func reverseIPv4(ip net.IP) string {
	return strconv.Itoa(int(ip[3])) + "." + strconv.Itoa(int(ip[2])) + "." +
		strconv.Itoa(int(ip[1])) + "." + strconv.Itoa(int(ip[0]))
}

// isPrivateIP reports whether ip is loopback, unspecified, link-local, RFC1918,
// CGNAT (100.64/10) or IPv6 ULA — none of which have a public ASN worth a lookup.
func isPrivateIP(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return true
	}
	if p.IsLoopback() || p.IsUnspecified() || p.IsLinkLocalUnicast() || p.IsLinkLocalMulticast() || p.IsPrivate() {
		return true
	}
	if v4 := p.To4(); v4 != nil {
		// 100.64.0.0/10 carrier-grade NAT
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
}
