package traffic

import (
	"context"
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
	SettingEgressEnabled             = "traffic_egress_enabled"              // master on/off (default true)
	SettingEgressRetentionDays       = "traffic_egress_retention_days"       // hot 5-min table TTL (default 7)
	SettingEgressHourlyRetentionDays = "traffic_egress_hourly_retention_days" // hourly rollup TTL (default 90)
	SettingEgressASNEnabled          = "traffic_egress_asn_enabled"          // Team Cymru DNS ASN lookups (default true)
	SettingEgressRDNSEnabled         = "traffic_egress_rdns_enabled"         // PTR reverse-DNS names for IP-only rows (default true)
	SettingEgressSocketSampler       = "traffic_egress_socket_sampler"       // ss-based universal sampler (default true)
	SettingEgressXrayAccessPath      = "traffic_egress_xray_access_path"     // path to a zenith-xray access.log to tail ("" = off)
	SettingEgressInstanceMap         = "traffic_egress_instance_map"         // JSON {procComm: instanceLabel} overrides for the sampler
	SettingEgressPruneHour           = "traffic_egress_prune_hour"           // local hour to run prune/rollup (default 5)
	SettingEgressRollupWatermark     = "traffic_egress_rollup_watermark"     // internal: last hour-bucket rolled up
)

const (
	bucketSecs        int64 = 300  // hot table bucket width
	hourSecs          int64 = 3600 // rollup bucket width
	defaultHotDays          = 7
	defaultHourlyDays       = 90
	defaultPruneHour        = 5
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

	mu      sync.Mutex
	pending map[destKey]destStat
}

// NewEgressCollector constructs the collector. agg may be nil under unit tests.
func NewEgressCollector(db *gorm.DB, agg *proxyAggregator) *EgressCollector {
	e := &EgressCollector{
		db:      db,
		agg:     agg,
		asn:     newASNResolver(getBoolSetting(SettingEgressASNEnabled, true)),
		rdns:    newRDNSResolver(getBoolSetting(SettingEgressRDNSEnabled, true)),
		pending: make(map[destKey]destStat),
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
	k := destKey{instance: instance, user: user, host: host, ip: ip, direction: direction}
	e.mu.Lock()
	s := e.pending[k]
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

	e.mu.Lock()
	pend := e.pending
	e.pending = make(map[destKey]destStat, len(pend))
	e.mu.Unlock()
	if len(pend) == 0 {
		return
	}

	bucket := (time.Now().Unix() / bucketSecs) * bucketSecs
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		for k, s := range pend {
			e.upsert(tx, bucket, k, s)
		}
		return nil
	}); err != nil {
		log.Printf("traffic egress flush: %v", err)
	}
}

var teUniqueCols = []clause.Column{
	{Name: "bucket"}, {Name: "instance"}, {Name: "user_email"},
	{Name: "dest_host"}, {Name: "dest_ip"}, {Name: "direction"},
}

func (e *EgressCollector) upsert(tx *gorm.DB, bucket int64, k destKey, s destStat) {
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
		log.Printf("traffic egress upsert: %v", err)
	}
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
		e.db.Model(&model.TrafficEgress{}).Select("COALESCE(MIN(bucket),0)").Scan(&minB)
		if minB == 0 {
			_ = config.SetSetting(SettingEgressRollupWatermark, strconv.FormatInt(target, 10))
			return nil
		}
		wm = (minB/hourSecs)*hourSecs - hourSecs
	}

	for h := wm + hourSecs; h <= target; h += hourSecs {
		var rows []model.TrafficEgressHourly
		// The hourly table drops dest_ip, so fold the best-effort rDNS name into
		// dest_host — otherwise every IP-only row would collapse to one blank
		// per-ASN row and the long-range view would lose all destination detail.
		e.db.Model(&model.TrafficEgress{}).
			Select("? as bucket, instance, user_email, COALESCE(NULLIF(dest_host,''), dest_rdns) as dest_host, asn, direction, "+
				"SUM(bytes_up) as bytes_up, SUM(bytes_down) as bytes_down, SUM(hits) as hits, "+
				"MAX(as_org) as as_org, MAX(country) as country", h).
			Where("bucket >= ? AND bucket < ?", h, h+hourSecs).
			Group("instance, user_email, COALESCE(NULLIF(dest_host,''), dest_rdns), asn, direction").
			Scan(&rows)
		if len(rows) > 0 {
			if err := e.db.Clauses(clause.OnConflict{Columns: tehUniqueCols, DoNothing: true}).
				CreateInBatches(&rows, 200).Error; err != nil {
				return err
			}
		}
		_ = config.SetSetting(SettingEgressRollupWatermark, strconv.FormatInt(h, 10))
	}
	return nil
}

// PruneHour returns the configured local hour (0-23) for the daily prune/rollup tick.
func PruneHour() int {
	return getIntSetting(SettingEgressPruneHour, defaultPruneHour, 0, 23)
}
