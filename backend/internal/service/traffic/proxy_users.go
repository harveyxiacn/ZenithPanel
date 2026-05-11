package traffic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
)

// clashConn is the subset of fields we need from Sing-box's Clash API response.
// We keep the unmarshalling permissive — the Clash schema has drifted across
// sing-box versions and we'd rather degrade gracefully than fail to parse.
type clashConn struct {
	ID       string            `json:"id"`
	Upload   uint64            `json:"upload"`
	Download uint64            `json:"download"`
	Start    string            `json:"start"`
	Metadata clashConnMetadata `json:"metadata"`
}

type clashConnMetadata struct {
	Network     string `json:"network"`
	Type        string `json:"type"`
	SourceIP    string `json:"sourceIP"`
	DestinationIP string `json:"destinationIP"`
	Host        string `json:"host"`
	InboundUser string `json:"inboundUser"`
	User        string `json:"user"`
	Process     string `json:"process"`
}

type clashConnectionsResponse struct {
	Connections []clashConn `json:"connections"`
}

// proxyAggregator turns sequential Clash API snapshots into per-user upload/
// download *rates*. Clash gives cumulative bytes per connection — we cache the
// previous values and diff.
type proxyAggregator struct {
	mu       sync.Mutex
	lastAt   time.Time
	lastConn map[string]clashConn // by connection id
	httpc    *http.Client
}

func newProxyAggregator() *proxyAggregator {
	return &proxyAggregator{
		lastConn: map[string]clashConn{},
		// Short timeout: the Clash API is local; if it hangs longer than this
		// the panel UI should see an error and we should not block the loop.
		httpc: &http.Client{Timeout: 2 * time.Second},
	}
}

// sample polls the Clash API once and returns per-user samples. If sing-box
// isn't running or the Clash API isn't enabled, we still return per-user
// totals from the Client table (with zero rates) so the page is useful in
// both states.
func (a *proxyAggregator) sample(sm *proxy.SingboxManager) ([]ProxyUserSample, error) {
	now := time.Now()

	clientTotals := loadClientTotals()
	users := make(map[string]*ProxyUserSample, len(clientTotals))
	for email, tot := range clientTotals {
		users[email] = &ProxyUserSample{
			Email:         email,
			UploadTotal:   tot.up,
			DownloadTotal: tot.down,
		}
	}

	connsByUser, byID, err := a.fetchConnections(sm)
	if err != nil {
		out := flatten(users)
		return out, err
	}

	// Compute deltas vs the last snapshot.
	a.mu.Lock()
	prev := a.lastConn
	dt := now.Sub(a.lastAt).Seconds()
	a.lastConn = byID
	a.lastAt = now
	a.mu.Unlock()

	if dt <= 0 || dt > 30 { // 30s gap → treat as cold start, skip rate
		dt = 0
	}

	for user, conns := range connsByUser {
		us, ok := users[user]
		if !ok {
			us = &ProxyUserSample{Email: user}
			users[user] = us
		}
		us.ActiveConns = len(conns)
		targets := map[string]struct{}{}
		var deltaUp, deltaDown uint64
		for _, c := range conns {
			if t := pickTarget(c); t != "" {
				targets[t] = struct{}{}
			}
			if dt > 0 {
				if old, ok := prev[c.ID]; ok {
					if c.Upload >= old.Upload {
						deltaUp += c.Upload - old.Upload
					}
					if c.Download >= old.Download {
						deltaDown += c.Download - old.Download
					}
				} else {
					// New connection — count whatever has already flowed since
					// it opened. Better signal than zero on the first tick.
					deltaUp += c.Upload
					deltaDown += c.Download
				}
			}
		}
		us.TopTargets = topNStrings(targets, 5)
		if dt > 0 {
			us.UploadRateBps = uint64(float64(deltaUp) / dt)
			us.DownloadRateBps = uint64(float64(deltaDown) / dt)
		}
	}

	return flatten(users), nil
}

func flatten(m map[string]*ProxyUserSample) []ProxyUserSample {
	out := make([]ProxyUserSample, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	// Sort by current rate desc, then total desc, then email asc so the busiest
	// user is on top — and the order is stable for users who are idle.
	sort.Slice(out, func(i, j int) bool {
		ri := out[i].UploadRateBps + out[i].DownloadRateBps
		rj := out[j].UploadRateBps + out[j].DownloadRateBps
		if ri != rj {
			return ri > rj
		}
		ti := out[i].UploadTotal + out[i].DownloadTotal
		tj := out[j].UploadTotal + out[j].DownloadTotal
		if ti != tj {
			return ti > tj
		}
		return out[i].Email < out[j].Email
	})
	return out
}

func pickTarget(c clashConn) string {
	if c.Metadata.Host != "" {
		return c.Metadata.Host
	}
	return c.Metadata.DestinationIP
}

func userOf(c clashConn) string {
	if c.Metadata.User != "" {
		return c.Metadata.User
	}
	return c.Metadata.InboundUser
}

func (a *proxyAggregator) fetchConnections(sm *proxy.SingboxManager) (map[string][]clashConn, map[string]clashConn, error) {
	if sm == nil || !sm.Status() {
		return nil, nil, fmt.Errorf("sing-box is not running")
	}
	if config.GetSetting("singbox_clash_api_enabled") != "true" {
		return nil, nil, fmt.Errorf("Clash API is not enabled — turn it on in Proxy settings and re-apply the config")
	}
	port := config.GetSetting("singbox_clash_api_port")
	if port == "" {
		port = "9090"
	}
	resp, err := a.httpc.Get("http://127.0.0.1:" + port + "/connections")
	if err != nil {
		return nil, nil, fmt.Errorf("clash api unreachable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, nil, err
	}
	var parsed clashConnectionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, fmt.Errorf("clash api response: %w", err)
	}
	byUser := make(map[string][]clashConn, len(parsed.Connections))
	byID := make(map[string]clashConn, len(parsed.Connections))
	for _, c := range parsed.Connections {
		if c.ID != "" {
			byID[c.ID] = c
		}
		u := userOf(c)
		if u == "" {
			u = "(anonymous)"
		}
		byUser[u] = append(byUser[u], c)
	}
	return byUser, byID, nil
}

type clientTotals struct {
	up   int64
	down int64
}

// loadClientTotals snapshots cumulative bytes for every enabled client so the
// per-user row shows lifetime usage alongside live rate. We swallow errors —
// missing totals just mean "show 0" rather than break the page.
func loadClientTotals() map[string]clientTotals {
	out := map[string]clientTotals{}
	if config.DB == nil {
		return out
	}
	var clients []model.Client
	config.DB.Where("enable = ?", true).Find(&clients)
	for _, c := range clients {
		out[c.Email] = clientTotals{up: c.UpLoad, down: c.DownLoad}
	}
	return out
}

func topNStrings(set map[string]struct{}, n int) []string {
	if len(set) == 0 {
		return nil
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}
