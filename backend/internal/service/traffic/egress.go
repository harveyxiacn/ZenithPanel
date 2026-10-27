package traffic

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Setting keys for the egress logger. All are stored in the existing Setting
// key-value table and editable from the panel UI / API.
const (
	SettingEgressEnabled             = "traffic_egress_enabled"               // master on/off (default true)
	SettingEgressRetentionDays       = "traffic_egress_retention_days"        // hot 5-min table TTL (default 7)
	SettingEgressHourlyRetentionDays = "traffic_egress_hourly_retention_days" // hourly rollup TTL (default 90)
	SettingEgressASNEnabled          = "traffic_egress_asn_enabled"           // Team Cymru DNS ASN lookups (default true)
	SettingEgressRDNSEnabled         = "traffic_egress_rdns_enabled"          // PTR reverse-DNS names for IP-only rows (default true)
	SettingEgressSocketSampler       = "traffic_egress_socket_sampler"        // ss-based universal sampler (default true)
	SettingEgressXrayAccessPath      = "traffic_egress_xray_access_path"      // path to a zenith-xray access.log to tail ("" = off)
	SettingEgressInstanceMap         = "traffic_egress_instance_map"          // JSON {procComm: instanceLabel} overrides for the sampler
	SettingEgressPruneHour           = "traffic_egress_prune_hour"            // local hour to run prune/rollup (default 5)
	SettingEgressRollupWatermark     = "traffic_egress_rollup_watermark"      // internal: last hour-bucket rolled up
)

const (
	bucketSecs           int64 = 300  // hot table bucket width
	hourSecs             int64 = 3600 // rollup bucket width
	defaultHotDays             = 7
	defaultHourlyDays          = 90
	defaultPruneHour           = 5
	maxEgressPendingKeys       = 20000
	egressWriteTimeout         = 5 * time.Second
)

func getBoolSetting(key string, def bool) bool {
	v := config.GetSetting(key)
	if v == "" {
		return def
	}
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

func getIntSetting(key string, def, min, max int) int {
	v := config.GetSetting(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return def
	}
	return n
}

// destKey is the in-memory aggregation key, matching the hot table's unique index.
type destKey struct {
	instance, user, host, ip, direction string
	bucket                              int64
}

type destStat struct{ up, down, hits int64 }

// EgressCollector aggregates per-(instance, user, destination, direction) byte
// deltas from several sources and upserts them into 5-minute buckets on the
// accountant's 30 s flush — one SQLite writer, no concurrent-writer lock churn.
//
// Sources:
//   - zenith-singbox: drained from the live Clash poller (proxyAggregator).
//   - socket sampler: ss-based, host-wide IP-level egress + return for the
//     other proxy processes (3x-ui, wireproxy, cpa, …).
//   - xray access-log tail (opt-in): domains+users for a zenith-xray access.log.
//   - sub2api Postgres (opt-in): upstream+user from its usage_logs table.
type EgressCollector struct {
	db   *gorm.DB
	agg  *proxyAggregator
	asn  *asnResolver
	rdns *rdnsResolver

	flushMu  sync.Mutex
	inFlight int
	mu       sync.Mutex
	pending  map[destKey]destStat
	retry    map[destKey]destStat
	dropped  uint64
	retries  uint64
	lastErr  string
}

// NewEgressCollector constructs the collector. agg may be nil under unit tests.
func NewEgressCollector(db *gorm.DB, agg *proxyAggregator) *EgressCollector {
	e := &EgressCollector{
		db:      db,
		agg:     agg,
		asn:     newASNResolver(getBoolSetting(SettingEgressASNEnabled, true)),
		rdns:    newRDNSResolver(getBoolSetting(SettingEgressRDNSEnabled, true)),
		pending: make(map[destKey]destStat),
		retry:   make(map[destKey]destStat),
	}
	egressActive.Store(getBoolSetting(SettingEgressEnabled, true))
	return e
}

// Add accumulates a byte delta for one (instance, user, destination, direction).
// Called by every source; safe for concurrent use.
func (e *EgressCollector) Add(instance, user, host, ip, direction string, up, down, hits int64) {
	if e == nil || (up == 0 && down == 0 && hits == 0) {
		return
	}
	if len(host) > 255 {
		host = host[:255]
	}
	if len(user) > 128 {
		user = user[:128]
	}
	if len(ip) > 45 {
		ip = ip[:45]
	}
	if len(instance) > 128 {
		instance = instance[:128]
	}
	if len(direction) > 32 {
		direction = direction[:32]
	}
	k := destKey{bucket: time.Now().Unix() / bucketSecs * bucketSecs, instance: instance, user: user, host: host, ip: ip, direction: direction}
	e.mu.Lock()
	s, exists := e.pending[k]
	if !exists && len(e.pending)+len(e.retry)+e.inFlight >= maxEgressPendingKeys {
		e.dropped++
		e.mu.Unlock()
		return
	}
	s.up += up
	s.down += down
	s.hits += hits
	e.pending[k] = s
	e.mu.Unlock()
}

// Flush is called from the accountant's 30 s flushOnce(). It pulls the Clash
// per-destination deltas, drains everything accumulated since the last flush,
// and upserts it into the current 5-minute bucket. It is the single writer to
// the traffic_egress table.
func (e *EgressCollector) Flush() {
	if e == nil || e.db == nil {
		return
	}
	e.flushMu.Lock()
	defer e.flushMu.Unlock()
	enabled := getBoolSetting(SettingEgressEnabled, true)
	egressActive.Store(enabled)

	// Always drain the Clash per-dest buffer so it can't grow unbounded when the
	// feature is toggled off mid-run.
	var destDrain map[destAggKey]pendingDelta
	if e.agg != nil {
		destDrain = e.agg.drainPendingDest()
	}
	if !enabled {
		e.mu.Lock()
		e.pending = make(map[destKey]destStat)
		e.retry = make(map[destKey]destStat)
		e.mu.Unlock()
		return
	}
	for k, d := range destDrain {
		// Teach the IP-only tiers this IP's sniffed domain — even a zero-delta
		// connection carries the mapping.
		if k.host != "" && k.ip != "" {
			e.rdns.learn(k.ip, k.host)
		}
		if d.up == 0 && d.down == 0 {
			continue
		}
		// Labeled by the engine name (matches the sing-box process comm), not a
		// hard-coded site-specific name.
		e.Add("sing-box", k.user, k.host, k.ip, "egress", int64(d.up), int64(d.down), 1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), egressWriteTimeout)
	defer cancel()
	for ctx.Err() == nil {
		// Limit each transaction so interactive writes can acquire the writer pool.
		batch := make(map[destKey]destStat)
		e.mu.Lock()
		for _, source := range []map[destKey]destStat{e.retry, e.pending} {
			for k, v := range source {
				if len(batch) >= 32 {
					break
				}
				old := batch[k]
				old.up += v.up
				old.down += v.down
				old.hits += v.hits
				batch[k] = old
				delete(source, k)
			}
		}
		e.inFlight = len(batch)
		e.mu.Unlock()
		if len(batch) == 0 {
			return
		}
		err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for k, v := range batch {
				if err := e.upsert(tx, k.bucket, k, v); err != nil {
					return err
				}
			}
			return nil
		})
		e.mu.Lock()
		e.inFlight = 0
		if err != nil {
			mergeDestStats(e.retry, batch)
			e.retries++
			e.lastErr = err.Error()
		} else {
			e.lastErr = ""
		}
		e.mu.Unlock()
		if err != nil {
			log.Printf("traffic egress flush: %v", err)
			return
		}
	}
}

var teUniqueCols = []clause.Column{
	{Name: "bucket"}, {Name: "instance"}, {Name: "user_email"},
	{Name: "dest_host"}, {Name: "dest_ip"}, {Name: "direction"},
}

func (e *EgressCollector) upsert(tx *gorm.DB, bucket int64, k destKey, s destStat) error {
	info := e.asn.lookup(k.ip)
	rdnsName := ""
	if k.host == "" {
		rdnsName = e.rdns.lookup(k.ip)
	}
	row := model.TrafficEgress{
		Bucket: bucket, Instance: k.instance, UserEmail: k.user,
		DestHost: k.host, DestIP: k.ip, DestRDNS: rdnsName, Direction: k.direction,
		ASN: info.asn, ASOrg: info.org, Country: info.country,
		BytesUp: s.up, BytesDown: s.down, Hits: s.hits,
	}
	assigns := map[string]interface{}{
		"bytes_up":   gorm.Expr("bytes_up + ?", s.up),
		"bytes_down": gorm.Expr("bytes_down + ?", s.down),
		"hits":       gorm.Expr("hits + ?", s.hits),
	}
	// Backfill ASN onto a row first written before the async resolver answered.
	if info.asn != "" {
		assigns["asn"] = info.asn
		assigns["as_org"] = info.org
		assigns["country"] = info.country
	}
	// Same for the reverse-DNS / learned domain name.
	if rdnsName != "" {
		assigns["dest_rdns"] = rdnsName
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   teUniqueCols,
		DoUpdates: clause.Assignments(assigns),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("upsert %s/%s: %w", k.instance, k.ip, err)
	}
	return nil
}

func mergeDestStats(dst, src map[destKey]destStat) {
	for key, stat := range src {
		cur := dst[key]
		cur.up += stat.up
		cur.down += stat.down
		cur.hits += stat.hits
		dst[key] = cur
	}
}

// EgressStatus is intentionally small so callers can surface collector health
// without exposing destination data.
type EgressStatus struct {
	Pending int    `json:"pending"`
	Retry   int    `json:"retry"`
	Dropped uint64 `json:"dropped_destination_keys"`
	Retries uint64 `json:"retries"`
	LastErr string `json:"last_error,omitempty"`
}

func (e *EgressCollector) Status() EgressStatus {
	if e == nil {
		return EgressStatus{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return EgressStatus{Pending: len(e.pending), Retry: len(e.retry), Dropped: e.dropped, Retries: e.retries, LastErr: e.lastErr}
}

// Start launches the background source goroutines. Each checks its own enable
// setting internally so they can be toggled at runtime, and all exit on ctx.Done.
//
// sub2api is intentionally not a dedicated source: its rich per-request data is
// tokens (not network bytes) and lives in its own dashboard, while its actual
// network egress is captured like any other process by the socket sampler
// (instance "sub2api", bytes by destination IP).
func (e *EgressCollector) Start(ctx context.Context) {
	go e.runSocketSampler(ctx)
	go e.runXrayAccess(ctx)
}

// ---- retention + rollup --------------------------------------------------

// PruneHot deletes hot 5-minute rows past the configured retention window.
func (e *EgressCollector) PruneHot(now time.Time) (int64, error) {
	if e.db == nil {
		return 0, nil
	}
	days := getIntSetting(SettingEgressRetentionDays, defaultHotDays, 1, 365)
	cutoff := now.AddDate(0, 0, -days).Unix()
	res := e.db.Where("bucket < ?", cutoff).Delete(&model.TrafficEgress{})
	return res.RowsAffected, res.Error
}

// PruneHourly deletes hourly rollup rows past the configured retention window.
func (e *EgressCollector) PruneHourly(now time.Time) (int64, error) {
	if e.db == nil {
		return 0, nil
	}
	days := getIntSetting(SettingEgressHourlyRetentionDays, defaultHourlyDays, 1, 3650)
	cutoff := now.AddDate(0, 0, -days).Unix()
	res := e.db.Where("bucket < ?", cutoff).Delete(&model.TrafficEgressHourly{})
	return res.RowsAffected, res.Error
}

var tehUniqueCols = []clause.Column{
	{Name: "bucket"}, {Name: "instance"}, {Name: "user_email"},
	{Name: "dest_host"}, {Name: "asn"}, {Name: "direction"},
}

// RollupOnce aggregates completed hot hours into the hourly table, advancing a
// persisted watermark so each hour is rolled exactly once. The OnConflict
// DoNothing makes a re-run after a mid-rollup crash idempotent.
func (e *EgressCollector) RollupOnce(now time.Time) error {
	if e.db == nil {
		return nil
	}
	curHour := (now.Unix() / hourSecs) * hourSecs
	target := curHour - hourSecs // only roll fully-elapsed hours

	var wm int64
	if raw := config.GetSetting(SettingEgressRollupWatermark); raw != "" {
		wm, _ = strconv.ParseInt(raw, 10, 64)
	}
	if wm == 0 {
		var minB int64
		if err := e.db.Model(&model.TrafficEgress{}).Select("COALESCE(MIN(bucket),0)").Scan(&minB).Error; err != nil {
			return fmt.Errorf("read egress rollup watermark: %w", err)
		}
		if minB == 0 {
			if err := config.SetSetting(SettingEgressRollupWatermark, strconv.FormatInt(target, 10)); err != nil {
				return fmt.Errorf("persist egress rollup watermark: %w", err)
			}
			return nil
		}
		wm = (minB/hourSecs)*hourSecs - hourSecs
	}

	for h := wm + hourSecs; h <= target; h += hourSecs {
		var rows []model.TrafficEgressHourly
		// The hourly table drops dest_ip, so fold the best-effort rDNS name into
		// dest_host — otherwise every IP-only row would collapse to one blank
		// per-ASN row and the long-range view would lose all destination detail.
		if err := e.db.Model(&model.TrafficEgress{}).
			Select("? as bucket, instance, user_email, COALESCE(NULLIF(dest_host,''), dest_rdns) as dest_host, asn, direction, "+
				"SUM(bytes_up) as bytes_up, SUM(bytes_down) as bytes_down, SUM(hits) as hits, "+
				"MAX(as_org) as as_org, MAX(country) as country", h).
			Where("bucket >= ? AND bucket < ?", h, h+hourSecs).
			Group("instance, user_email, COALESCE(NULLIF(dest_host,''), dest_rdns), asn, direction").
			Scan(&rows).Error; err != nil {
			return fmt.Errorf("read egress rollup bucket %d: %w", h, err)
		}
		if len(rows) > 0 {
			if err := e.db.Clauses(clause.OnConflict{Columns: tehUniqueCols, DoNothing: true}).
				CreateInBatches(&rows, 200).Error; err != nil {
				return err
			}
		}
		if err := config.SetSetting(SettingEgressRollupWatermark, strconv.FormatInt(h, 10)); err != nil {
			return fmt.Errorf("persist egress rollup watermark for bucket %d: %w", h, err)
		}
	}
	return nil
}

// PruneHour returns the configured local hour (0-23) for the daily prune/rollup tick.
func PruneHour() int {
	return getIntSetting(SettingEgressPruneHour, defaultPruneHour, 0, 23)
}
