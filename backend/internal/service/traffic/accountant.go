package traffic

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
	"gorm.io/gorm"
)

// Accountant turns engine-level traffic counters into durable per-Client
// UpLoad/DownLoad numbers. Two sources of truth, one DB target:
//
//   - Sing-box: byte deltas already computed by proxyAggregator on every UI
//     poll are drained every 30 s and added to the Client row.
//   - Xray: every 30 s we exec "xray api statsquery -reset" against the
//     internal API inbound, then add each returned counter to the Client row.
//     "-reset" zeroes the counter on the Xray side so the next read is a
//     delta — no separate previous-snapshot bookkeeping needed.
//
// Both paths upsert against Client(email) using a GORM Updates clause so
// repeated zero deltas are no-ops and concurrent writes serialize on the
// SQLite-level lock. The accountant exits when ctx is cancelled.
type Accountant struct {
	db         *gorm.DB
	xm         *proxy.XrayManager
	sm         *proxy.SingboxManager
	agg        *proxyAggregator
	interval   time.Duration
	mu         sync.RWMutex
	lastFlush  time.Time
	lastXrayOK time.Time
	lastErr    string
}

// NewAccountant wires the accountant to the running managers and the proxy
// aggregator that already polls Clash API. db may be nil under unit tests;
// the accountant treats nil as "no-op" and just exercises the polling code.
func NewAccountant(db *gorm.DB, xm *proxy.XrayManager, sm *proxy.SingboxManager, agg *proxyAggregator) *Accountant {
	return &Accountant{
		db:       db,
		xm:       xm,
		sm:       sm,
		agg:      agg,
		interval: 30 * time.Second,
	}
}

// Start launches the periodic flush goroutine. The first tick fires after
// `interval`; we deliberately don't flush on boot so a daily-reset that just
// zeroed counters isn't immediately overwritten by stale buffered bytes.
func (a *Accountant) Start(ctx context.Context) {
	go a.loop(ctx)
}

func (a *Accountant) loop(ctx context.Context) {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.flushOnce()
		}
	}
}

func (a *Accountant) flushOnce() {
	a.flushSingbox()
	a.flushXray()
	a.mu.Lock()
	a.lastFlush = time.Now()
	a.mu.Unlock()
}

func (a *Accountant) flushSingbox() {
	if a.agg == nil {
		return
	}
	pending := a.agg.drainPending()
	if len(pending) == 0 {
		return
	}
	a.applyDeltas(pending, "singbox")
}

func (a *Accountant) flushXray() {
	if a.xm == nil || !a.xm.Status() {
		return
	}
	stats, err := queryXrayStats(proxy.XrayStatsAPIPort())
	if err != nil {
		a.mu.Lock()
		a.lastErr = err.Error()
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.lastXrayOK = time.Now()
	a.lastErr = ""
	a.mu.Unlock()
	if len(stats) == 0 {
		return
	}
	a.applyDeltas(stats, "xray")
}

// applyDeltas adds per-email byte counts to the Client table. Skips users that
// don't match any Client row — those are typically "(anonymous)" connections
// or stale email keys from a previous config. Logs once per source per flush
// when nothing matched, so a misconfigured stats inbound surfaces clearly.
func (a *Accountant) applyDeltas(deltas map[string]pendingDelta, source string) {
	if a.db == nil {
		return
	}
	matched := 0
	for email, d := range deltas {
		if email == "" || email == "(anonymous)" {
			continue
		}
		if d.up == 0 && d.down == 0 {
			continue
		}
		res := a.db.Model(&model.Client{}).
			Where("email = ?", email).
			Updates(map[string]interface{}{
				"up_load":   gorm.Expr("up_load + ?", d.up),
				"down_load": gorm.Expr("down_load + ?", d.down),
			})
		if res.Error != nil {
			log.Printf("traffic accountant (%s): update %s: %v", source, email, res.Error)
			continue
		}
		matched += int(res.RowsAffected)
	}
	if matched == 0 && len(deltas) > 0 {
		log.Printf("traffic accountant (%s): %d deltas had no matching Client rows", source, len(deltas))
	}
}

// Status returns last-flush metadata for the diagnostic endpoint. Used by the
// frontend to show "no Xray stats yet — re-apply config" when the panel was
// upgraded but Xray is still running with the pre-update config.
func (a *Accountant) Status() AccountantStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return AccountantStatus{
		LastFlushAt:  a.lastFlush,
		LastXrayOKAt: a.lastXrayOK,
		LastError:    a.lastErr,
	}
}

type AccountantStatus struct {
	LastFlushAt  time.Time `json:"last_flush_at"`
	LastXrayOKAt time.Time `json:"last_xray_ok_at"`
	LastError    string    `json:"last_error,omitempty"`
}

// xrayStatsResponse mirrors the JSON shape emitted by "xray api statsquery".
// We only care about the name/value pair so other fields are ignored.
type xrayStatsResponse struct {
	Stat []xrayStatEntry `json:"stat"`
}

type xrayStatEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// queryXrayStats execs `xray api statsquery -reset` against the loopback API
// inbound and returns per-user byte deltas. "-reset" zeroes Xray's counter on
// read so each call returns just-since-last-call traffic — saves us from
// maintaining a previous-snapshot state map. Patterns are "user>>>EMAIL>>>traffic>>>uplink|downlink".
func queryXrayStats(port int) (map[string]pendingDelta, error) {
	server := "127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command("xray", "api", "statsquery",
		"--server="+server, "-pattern", "user>>>", "-reset")
	out, err := cmd.Output()
	if err != nil {
		// statsquery with no matching counters exits with status != 0 on some
		// xray builds; treat empty-output errors as "no data yet."
		if len(out) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("xray api statsquery: %w", err)
	}
	return parseXrayStats(out)
}

func parseXrayStats(raw []byte) (map[string]pendingDelta, error) {
	// statsquery output is sometimes wrapped in a {"stat":[...]} envelope and
	// sometimes a bare array. Try both.
	out := map[string]pendingDelta{}
	tryEnvelope := func(b []byte) bool {
		var env xrayStatsResponse
		if err := json.Unmarshal(b, &env); err != nil {
			return false
		}
		ingestXrayStatEntries(env.Stat, out)
		return true
	}
	tryArray := func(b []byte) bool {
		var arr []xrayStatEntry
		if err := json.Unmarshal(b, &arr); err != nil {
			return false
		}
		ingestXrayStatEntries(arr, out)
		return true
	}
	if !tryEnvelope(raw) && !tryArray(raw) {
		return nil, fmt.Errorf("unrecognized xray statsquery output: %.200s", string(raw))
	}
	return out, nil
}

// ingestXrayStatEntries maps each "user>>>EMAIL>>>traffic>>>uplink|downlink"
// counter into the right slot in the accumulator. Counters that don't match
// the user pattern (e.g., inbound>>> or outbound>>>) are ignored.
func ingestXrayStatEntries(entries []xrayStatEntry, out map[string]pendingDelta) {
	const userPrefix = "user>>>"
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, userPrefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(e.Name, userPrefix), ">>>")
		if len(parts) < 3 {
			continue
		}
		email := parts[0]
		direction := parts[2] // uplink / downlink
		bytes, err := strconv.ParseUint(strings.TrimSpace(e.Value), 10, 64)
		if err != nil || bytes == 0 {
			continue
		}
		pd := out[email]
		switch direction {
		case "uplink":
			pd.up += bytes
		case "downlink":
			pd.down += bytes
		default:
			continue
		}
		out[email] = pd
	}
}

// _ ensures config import stays referenced even after future refactors —
// xray_stats_port is read by proxy.XrayStatsAPIPort which lives in the
// proxy package, but we keep the package imported here for symmetry and to
// surface the setting key in one place if we later add a /traffic/settings
// endpoint.
var _ = config.GetSetting
