package traffic

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newEgressTestDB opens an in-memory SQLite, migrates the egress tables plus
// Setting (the collector reads its toggles via config.GetSetting), and points
// the config.DB global at it.
func newEgressTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	// One connection: each :memory: connection is its own database.
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.Setting{}, &model.TrafficEgress{}, &model.TrafficEgressHourly{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	config.DB = db
	return db
}

// newTestCollector builds a collector whose DNS-touching resolvers are
// disabled so tests never hit the network.
func newTestCollector(t *testing.T) *EgressCollector {
	t.Helper()
	e := NewEgressCollector(newEgressTestDB(t), nil)
	e.asn = newASNResolver(false)
	e.rdns = newRDNSResolver(false)
	return e
}

func TestRDNSLearnAndLookup(t *testing.T) {
	r := newRDNSResolver(false) // disabled: no worker, no PTR lookups

	r.learn("93.184.216.34", "Example.COM.")
	if got := r.lookup("93.184.216.34"); got != "example.com" {
		t.Errorf("learned lookup = %q, want example.com", got)
	}
	// A "host" that is itself an IP teaches nothing.
	r.learn("5.6.7.8", "9.9.9.9")
	if got := r.lookup("5.6.7.8"); got != "" {
		t.Errorf("IP-as-host should not be learned, got %q", got)
	}
	// Unknown IP on a disabled resolver stays empty (and must not block).
	if got := r.lookup("8.8.8.8"); got != "" {
		t.Errorf("unknown lookup = %q, want empty", got)
	}
	// Private IPs never resolve.
	r.learn("10.0.0.1", "internal.lan")
	if got := r.lookup("10.0.0.1"); got != "" {
		t.Errorf("private IP lookup = %q, want empty", got)
	}
	// A learned mapping survives a PTR result racing in via the worker path.
	r.mu.Lock()
	if cur := r.cache["93.184.216.34"]; !cur.learned {
		t.Errorf("learned flag lost: %+v", cur)
	}
	r.mu.Unlock()
}

func TestSummaryDestCoalescesDomainRDNSThenIP(t *testing.T) {
	e := newTestCollector(t)
	e.rdns.learn("93.184.216.34", "example.org")

	// IP-only tier hitting a learned IP, an unknown IP, and a domain tier row.
	e.Add("3x-ui", "", "", "93.184.216.34", "egress", 100, 200, 1)
	e.Add("3x-ui", "", "", "8.8.4.4", "egress", 10, 20, 1)
	e.Add("sing-box", "u@x", "sniffed.example.com", "1.2.3.4", "egress", 1000, 2000, 1)
	e.Flush()

	now := time.Now().Unix()
	rows := e.Summary(EgressFilter{Start: now - 3600, End: now + 3600}, "dest")
	byKey := map[string]EgressSummaryRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if r := byKey["sniffed.example.com"]; r.Kind != "domain" || r.BytesTotal != 3000 {
		t.Errorf("sniffed row = %+v, want kind=domain total=3000", r)
	}
	if r := byKey["example.org"]; r.Kind != "rdns" || r.BytesTotal != 300 {
		t.Errorf("rdns row = %+v, want kind=rdns total=300", r)
	}
	if r := byKey["8.8.4.4"]; r.Kind != "ip" || r.BytesTotal != 30 {
		t.Errorf("ip row = %+v, want kind=ip total=30", r)
	}
}

func TestUpsertBackfillsRDNSOnLaterFlush(t *testing.T) {
	e := newTestCollector(t)

	// First flush: mapping not yet known — row lands with empty dest_rdns.
	e.Add("3x-ui", "", "", "93.184.216.34", "egress", 1, 1, 1)
	e.Flush()
	// Mapping learned between flushes (e.g. Clash saw the SNI).
	e.rdns.learn("93.184.216.34", "example.org")
	e.Add("3x-ui", "", "", "93.184.216.34", "egress", 1, 1, 1)
	e.Flush()

	var row model.TrafficEgress
	if err := e.db.Where("dest_ip = ?", "93.184.216.34").First(&row).Error; err != nil {
		t.Fatalf("row not found: %v", err)
	}
	if row.DestRDNS != "example.org" {
		t.Errorf("dest_rdns = %q, want example.org (backfilled)", row.DestRDNS)
	}
	if row.BytesUp != 2 || row.BytesDown != 2 {
		t.Errorf("bytes not accumulated: %+v", row)
	}
}

func TestRollupFoldsRDNSIntoDestHost(t *testing.T) {
	e := newTestCollector(t)
	now := time.Now()
	h := (now.Unix()/3600)*3600 - 2*3600 // a fully-elapsed hour

	for _, b := range []int64{h, h + 300} {
		e.db.Create(&model.TrafficEgress{
			Bucket: b, Instance: "3x-ui", DestIP: "1.1.1.1",
			DestRDNS: "one.one.one.one", Direction: "egress",
			BytesUp: 5, BytesDown: 5, Hits: 1,
		})
	}
	if err := e.RollupOnce(now); err != nil {
		t.Fatalf("rollup: %v", err)
	}
	var rows []model.TrafficEgressHourly
	e.db.Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("hourly rows = %d, want 1 (%+v)", len(rows), rows)
	}
	if rows[0].DestHost != "one.one.one.one" {
		t.Errorf("hourly dest_host = %q, want folded rdns name", rows[0].DestHost)
	}
	if rows[0].BytesUp != 10 || rows[0].BytesDown != 10 {
		t.Errorf("hourly bytes = %+v, want 10/10", rows[0])
	}
}
