package traffic

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

func TestParseXrayStatsEnvelope(t *testing.T) {
	raw := []byte(`{"stat":[
		{"name":"user>>>alice@x>>>traffic>>>uplink","value":"1024"},
		{"name":"user>>>alice@x>>>traffic>>>downlink","value":"2048"},
		{"name":"user>>>bob@y>>>traffic>>>uplink","value":"500"},
		{"name":"inbound>>>vless>>>traffic>>>uplink","value":"9999"}
	]}`)
	got, err := parseXrayStats(raw)
	if err != nil {
		t.Fatalf("parseXrayStats: %v", err)
	}
	if got["alice@x"].up != 1024 || got["alice@x"].down != 2048 {
		t.Errorf("alice: got %+v", got["alice@x"])
	}
	if got["bob@y"].up != 500 || got["bob@y"].down != 0 {
		t.Errorf("bob: got %+v", got["bob@y"])
	}
	if _, has := got["vless"]; has {
		t.Errorf("inbound-scoped counters should not be attributed to users")
	}
}

func TestParseXrayStatsBareArray(t *testing.T) {
	raw := []byte(`[
		{"name":"user>>>solo@x>>>traffic>>>uplink","value":"42"}
	]`)
	got, err := parseXrayStats(raw)
	if err != nil {
		t.Fatalf("parseXrayStats: %v", err)
	}
	if got["solo@x"].up != 42 {
		t.Errorf("expected 42, got %+v", got["solo@x"])
	}
}

func TestParseXrayStatsIgnoresZerosAndMalformed(t *testing.T) {
	raw := []byte(`{"stat":[
		{"name":"user>>>zero@x>>>traffic>>>uplink","value":"0"},
		{"name":"user>>>broken@x>>>traffic>>>uplink","value":"not-a-number"},
		{"name":"user>>>good@x>>>traffic>>>uplink","value":"100"}
	]}`)
	got, _ := parseXrayStats(raw)
	if _, has := got["zero@x"]; has {
		t.Errorf("zero values should not create a user entry")
	}
	if _, has := got["broken@x"]; has {
		t.Errorf("malformed value should be skipped")
	}
	if got["good@x"].up != 100 {
		t.Errorf("good@x not recorded")
	}
}

func TestParseXrayStatsRejectsGarbage(t *testing.T) {
	if _, err := parseXrayStats([]byte("not json at all")); err == nil {
		t.Errorf("expected error for non-JSON input")
	}
}

func TestApplyDeltasUpdatesClientRows(t *testing.T) {
	db := newTestDB(t)
	db.Create(&model.Client{Email: "alice@x", InboundID: 1, UpLoad: 100, DownLoad: 200, Enable: true})
	db.Create(&model.Client{Email: "bob@y", InboundID: 1, UpLoad: 0, DownLoad: 0, Enable: true})

	a := &Accountant{db: db}
	a.applyDeltas(map[string]pendingDelta{
		"alice@x":       {up: 50, down: 75},
		"bob@y":         {up: 1000, down: 2000},
		"ghost@nowhere": {up: 999, down: 999}, // no matching row — should be skipped silently
		"(anonymous)":   {up: 5, down: 5},     // ignored by convention
	}, "test")

	var alice model.Client
	db.Where("email = ?", "alice@x").First(&alice)
	if alice.UpLoad != 150 || alice.DownLoad != 275 {
		t.Errorf("alice not accumulated: up=%d down=%d", alice.UpLoad, alice.DownLoad)
	}
	var bob model.Client
	db.Where("email = ?", "bob@y").First(&bob)
	if bob.UpLoad != 1000 || bob.DownLoad != 2000 {
		t.Errorf("bob not accumulated: up=%d down=%d", bob.UpLoad, bob.DownLoad)
	}
}

func TestApplyDeltasIsNoOpWithNilDB(t *testing.T) {
	a := &Accountant{db: nil}
	// Must not panic.
	a.applyDeltas(map[string]pendingDelta{"alice@x": {up: 1, down: 1}}, "test")
}

func TestEngineForProtocolXrayProtocols(t *testing.T) {
	for _, p := range []string{"vless", "vmess", "trojan", "shadowsocks"} {
		if engineForProtocol(p) != "xray" {
			t.Errorf("expected %s → xray, got %s", p, engineForProtocol(p))
		}
	}
	for _, p := range []string{"hysteria2", "tuic", "wireguard", ""} {
		if engineForProtocol(p) != "singbox" {
			t.Errorf("expected %s → singbox, got %s", p, engineForProtocol(p))
		}
	}
}

func TestProxyAggregatorDrainPending(t *testing.T) {
	a := newProxyAggregator()
	a.mu.Lock()
	a.pendingFlush["alice@x"] = pendingDelta{up: 10, down: 20}
	a.pendingFlush["bob@y"] = pendingDelta{up: 1, down: 2}
	a.mu.Unlock()

	got := a.drainPending()
	if len(got) != 2 {
		t.Fatalf("expected 2 drained, got %d", len(got))
	}
	if got["alice@x"].up != 10 {
		t.Errorf("alice up wrong: %d", got["alice@x"].up)
	}
	// Second drain returns empty — confirming clear-on-read.
	if again := a.drainPending(); len(again) != 0 {
		t.Errorf("second drain should be empty, got %d entries", len(again))
	}
}

func TestAccountantStatusDoesNotRaceOnLoop(t *testing.T) {
	a := &Accountant{interval: 100 * time.Millisecond}
	// Just exercise the status read path on a fresh accountant.
	if s := a.Status(); !s.LastFlushAt.IsZero() {
		t.Errorf("fresh accountant should have zero LastFlushAt")
	}
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Client{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
