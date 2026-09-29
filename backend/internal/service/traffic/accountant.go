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
//   - Xray: every 30 s (and right before Xray is stopped) we exec
//     "xray api statsquery" against the internal API inbound and add the
//     growth of each cumulative counter since the last persisted value.
//
// Both paths update Client rows by ID, resolved from the per-client stats
// identity the engine configs use (proxy.StatsIdentities). The accountant
// exits when ctx is cancelled.
type Accountant struct {
	db         *gorm.DB
	xm         *proxy.XrayManager
	sm         *proxy.SingboxManager
	agg        *proxyAggregator
	egress     *EgressCollector
	interval   time.Duration
	mu         sync.RWMutex
	lastFlush  time.Time
	lastXrayOK time.Time
	lastErr    string

	// Xray counters are read cumulatively (no -reset) and diffed against
	// the last values we successfully persisted, so a failed DB write is
	// retried on the next flush instead of being lost. xrayStart identifies
	// the Xray process the baseline belongs to; a restart zeroes Xray's
	// counters, so the baseline is dropped when the start time changes.
	xrayMu    sync.Mutex
	xrayLast  map[string]pendingDelta
	xrayStart time.Time
}

// NewAccountant wires the accountant to the running managers and the proxy
// aggregator that already polls Clash API. db may be nil under unit tests;
// the accountant treats nil as "no-op" and just exercises the polling code.
// egress may be nil — the per-destination egress flush is then skipped.
func NewAccountant(db *gorm.DB, xm *proxy.XrayManager, sm *proxy.SingboxManager, agg *proxyAggregator, egress *EgressCollector) *Accountant {
	a := &Accountant{
		db:       db,
		xm:       xm,
		sm:       sm,
		agg:      agg,
		egress:   egress,
		interval: 30 * time.Second,
		xrayLast: map[string]pendingDelta{},
	}
	if xm != nil {
		// Capture Xray's in-memory counters before any stop/restart (e.g.
		// "apply"), which would otherwise discard up to one interval of
		// traffic.
		xm.SetBeforeStop(a.flushXray)
	}
	return a
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
	// Per-destination egress aggregation rides the same 30s cadence so there is
	// exactly one writer to the egress tables (no concurrent-writer lock churn).
	if a.egress != nil {
		a.egress.Flush()
	}
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
	settled := a.applyDeltas(pending, "singbox")
	for ident, d := range pending {
		if !settled[ident] {
			a.agg.requeue(ident, d) // DB error: retry on the next flush
		}
	}
}

func (a *Accountant) flushXray() {
	if a.xm == nil || !a.xm.Status() {
		return
	}
	a.xrayMu.Lock()
	defer a.xrayMu.Unlock()

	// Engine start time (to ~1 s): a different value means a new process
	// whose counters restarted from zero.
	start := time.Now().Add(-a.xm.Uptime()).Truncate(time.Second)
	if d := start.Sub(a.xrayStart); d > time.Second || d < -time.Second {
		a.xrayLast = map[string]pendingDelta{}
		a.xrayStart = start
	}

	totals, err := queryXrayStats(proxy.XrayStatsAPIPort())
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

	deltas := xrayDeltas(totals, a.xrayLast)
	for ident := range a.applyDeltas(deltas, "xray") {
		a.xrayLast[ident] = totals[ident]
	}
	// Identities with no new traffic are already in sync.
	for ident, t := range totals {
		if _, ok := deltas[ident]; !ok {
			a.xrayLast[ident] = t
		}
	}
}

// xrayDeltas diffs cumulative Xray counters against the last persisted
// values. A counter below its baseline means it was reset, so the whole
// current value is new traffic.
func xrayDeltas(totals, last map[string]pendingDelta) map[string]pendingDelta {
	out := map[string]pendingDelta{}
	for ident, t := range totals {
		l := last[ident]
		var d pendingDelta
		if t.up >= l.up {
			d.up = t.up - l.up
		} else {
			d.up = t.up
		}
		if t.down >= l.down {
			d.down = t.down - l.down
		} else {
			d.down = t.down
		}
		if d.up > 0 || d.down > 0 {
			out[ident] = d
		}
	}
	return out
}

// applyDeltas adds per-user byte counts to the Client table and returns the
// identities that are settled (written, or unattributable and deliberately
// dropped); identities hit by a DB error are omitted so callers can retry.
//
// Engines report traffic per stats identity (see proxy.StatsIdentities),
// which maps to exactly one Client row — updating by email instead credited
// the same bytes to every row sharing that email.
func (a *Accountant) applyDeltas(deltas map[string]pendingDelta, source string) map[string]bool {
	settled := make(map[string]bool, len(deltas))
	if a.db == nil {
		return settled
	}
	var clients []model.Client
	a.db.Select("id", "email").Find(&clients)
	byIdent := map[string]uint{}
	for id, ident := range proxy.StatsIdentities(clients) {
		byIdent[ident] = id
	}
	matched, unmatched := 0, 0
	for ident, d := range deltas {
		id, ok := byIdent[ident]
		if !ok {
			// "(anonymous)" connections, deleted clients, or stale names.
			settled[ident] = true
			if ident != "" && ident != "(anonymous)" {
				unmatched++
			}
			continue
		}
		if d.up == 0 && d.down == 0 {
			settled[ident] = true
			continue
		}
		res := a.db.Model(&model.Client{}).
			Where("id = ?", id).
			Updates(map[string]any{
				"up_load":   gorm.Expr("up_load + ?", d.up),
				"down_load": gorm.Expr("down_load + ?", d.down),
			})
		if res.Error != nil {
			log.Printf("traffic accountant (%s): update %s: %v", source, ident, res.Error)
			continue
		}
		settled[ident] = true
		matched += int(res.RowsAffected)
	}
	if matched == 0 && unmatched > 0 {
		log.Printf("traffic accountant (%s): %d deltas had no matching Client rows", source, unmatched)
	}
	return settled
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
	Name  string        `json:"name"`
	Value xrayStatValue `json:"value"`
}

// xrayStatValue accepts both encodings of a counter: older Xray releases
// print "value": "1024" (string), v26+ prints "value": 1024 (number).
// Decoding the number into a string field failed the whole response, which
// silently stopped all Xray per-user accounting after the v26 bump.
type xrayStatValue string

func (v *xrayStatValue) UnmarshalJSON(b []byte) error {
	*v = xrayStatValue(strings.Trim(string(b), `"`))
	return nil
}

// queryXrayStats execs `xray api statsquery -reset` against the loopback API
// inbound and returns per-user byte deltas. "-reset" zeroes Xray's counter on
// read so each call returns just-since-last-call traffic — saves us from
// maintaining a previous-snapshot state map. Patterns are "user>>>EMAIL>>>traffic>>>uplink|downlink".
func queryXrayStats(port int) (map[string]pendingDelta, error) {
	server := "127.0.0.1:" + strconv.Itoa(port)
	// No -reset: the accountant diffs cumulative values itself so that a
	// failed DB write doesn't lose the bytes Xray already zeroed.
	cmd := exec.Command("xray", "api", "statsquery",
		"--server="+server, "-pattern", "user>>>")
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
		bytes, err := strconv.ParseUint(strings.TrimSpace(string(e.Value)), 10, 64)
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
