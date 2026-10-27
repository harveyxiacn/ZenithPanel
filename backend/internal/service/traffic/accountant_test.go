package traffic

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/proxy"
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

func TestApplyDeltasUsesStableClientIdentityAndSkipsAmbiguousLegacyEmail(t *testing.T) {
	db := newTestDB(t)
	first := model.Client{Email: "same@example", InboundID: 1, Enable: true}
	second := model.Client{Email: "same@example", InboundID: 2, Enable: true}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	a := &Accountant{db: db}
	if settled := a.applyDeltas(map[string]pendingDelta{proxy.ClientIdentity(first.ID): {up: 100, down: 200}}, "test"); !settled[proxy.ClientIdentity(first.ID)] {
		t.Fatal("canonical identity was not settled")
	}
	// An old engine config can still emit an email. It must not double-charge
	// both rows once that email is no longer globally unique.
	if settled := a.applyDeltas(map[string]pendingDelta{"same@example": {up: 50}}, "test"); !settled["same@example"] {
		t.Fatal("unmatched legacy identity was not settled")
	}
	var gotFirst, gotSecond model.Client
	db.First(&gotFirst, first.ID)
	db.First(&gotSecond, second.ID)
	if gotFirst.UpLoad != 100 || gotFirst.DownLoad != 200 {
		t.Fatalf("first=%+v", gotFirst)
	}
	if gotSecond.UpLoad != 0 || gotSecond.DownLoad != 0 {
		t.Fatalf("second=%+v", gotSecond)
	}
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

// Regression: the same email on two inbounds (one row per protocol) used to
// receive every delta twice because updates matched on email.
func TestApplyDeltasDuplicateEmailCreditsOneRow(t *testing.T) {
	db := newTestDB(t)
	c1 := model.Client{Email: "carol", InboundID: 1, Enable: true}
	c2 := model.Client{Email: "carol", InboundID: 2, Enable: true}
	db.Create(&c1)
	db.Create(&c2)

	a := &Accountant{db: db}
	ident2 := fmt.Sprintf("carol#%d", c2.ID)
	settled := a.applyDeltas(map[string]pendingDelta{ident2: {up: 10, down: 20}}, "test")
	if !settled[ident2] {
		t.Fatalf("delta not settled")
	}
	var r1, r2 model.Client
	db.First(&r1, c1.ID)
	db.First(&r2, c2.ID)
	if r1.UpLoad != 0 || r1.DownLoad != 0 {
		t.Errorf("row 1 wrongly credited: %d/%d", r1.UpLoad, r1.DownLoad)
	}
	if r2.UpLoad != 10 || r2.DownLoad != 20 {
		t.Errorf("row 2 not credited: %d/%d", r2.UpLoad, r2.DownLoad)
	}
}

func TestXrayDeltas(t *testing.T) {
	last := map[string]pendingDelta{"a": {up: 100, down: 1000}, "b": {up: 50, down: 50}}
	totals := map[string]pendingDelta{
		"a": {up: 150, down: 1000}, // grew
		"b": {up: 10, down: 5},     // below baseline → counter was reset
		"c": {up: 7, down: 8},      // new identity
	}
	got := xrayDeltas(totals, last)
	if got["a"] != (pendingDelta{up: 50, down: 0}) {
		t.Errorf("a = %+v", got["a"])
	}
	if got["b"] != (pendingDelta{up: 10, down: 5}) {
		t.Errorf("b (reset) = %+v", got["b"])
	}
	if got["c"] != (pendingDelta{up: 7, down: 8}) {
		t.Errorf("c = %+v", got["c"])
	}
	if _, ok := xrayDeltas(map[string]pendingDelta{"a": {up: 100, down: 1000}}, last)["a"]; ok {
		t.Errorf("unchanged counter must produce no delta")
	}
}

func TestExternalXrayBaselineSurvivesRestartAndRetriesAtomically(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	client := model.Client{Email: "external@example", InboundID: 1, Enable: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	ident := proxy.ClientIdentity(client.ID)
	firstEpoch := time.Unix(1_700_000_000, 0)
	preMigrationTotals := map[string]pendingDelta{ident: {up: 100, down: 200}}

	// The first panel that knows about a still-running external runner seeds
	// the old cumulative values instead of charging its entire history.
	a1 := &Accountant{db: db, xrayLast: map[string]pendingDelta{}}
	if err := a1.persistExternalXraySnapshot(preMigrationTotals, nil, firstEpoch); err != nil {
		t.Fatalf("seed external baseline: %v", err)
	}
	assertClientTraffic(t, db, client.ID, 0, 0)

	// A replacement panel restores the baseline. Equal counters must not be
	// charged again; only the delta after the replacement is added.
	a2 := &Accountant{db: db}
	if err := a2.loadExternalXrayBaseline(); err != nil {
		t.Fatalf("restore external baseline: %v", err)
	}
	if err := a2.persistExternalXraySnapshot(preMigrationTotals, xrayDeltas(preMigrationTotals, a2.xrayLast), firstEpoch); err != nil {
		t.Fatalf("persist unchanged totals: %v", err)
	}
	assertClientTraffic(t, db, client.ID, 0, 0)
	grewTotals := map[string]pendingDelta{ident: {up: 130, down: 220}}
	if err := a2.persistExternalXraySnapshot(grewTotals, xrayDeltas(grewTotals, a2.xrayLast), firstEpoch); err != nil {
		t.Fatalf("persist growth: %v", err)
	}
	assertClientTraffic(t, db, client.ID, 30, 20)

	// The runner epoch changes when its container restarts. Its new cumulative
	// counters are all new traffic, rather than a negative diff from the old
	// process.
	secondEpoch := firstEpoch.Add(time.Hour)
	restartedTotals := map[string]pendingDelta{ident: {up: 5, down: 7}}
	if err := a2.persistExternalXraySnapshot(restartedTotals, xrayDeltas(restartedTotals, map[string]pendingDelta{}), secondEpoch); err != nil {
		t.Fatalf("persist restarted runner: %v", err)
	}
	assertClientTraffic(t, db, client.ID, 35, 27)

	// Baseline and Client increments share one transaction. A failed baseline
	// write rolls back the Client update and leaves the in-memory snapshot at
	// the old value, making the next attempt safe to replay.
	if err := db.Migrator().DropTable(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	retryTotals := map[string]pendingDelta{ident: {up: 9, down: 11}}
	if err := a2.persistExternalXraySnapshot(retryTotals, xrayDeltas(retryTotals, a2.xrayLast), secondEpoch); err == nil {
		t.Fatal("expected snapshot write to fail without settings table")
	}
	assertClientTraffic(t, db, client.ID, 35, 27)
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	if err := a2.persistExternalXraySnapshot(retryTotals, xrayDeltas(retryTotals, a2.xrayLast), secondEpoch); err != nil {
		t.Fatalf("retry snapshot write: %v", err)
	}
	assertClientTraffic(t, db, client.ID, 39, 31)
}

func assertClientTraffic(t *testing.T, db *gorm.DB, id uint, up, down int64) {
	t.Helper()
	var client model.Client
	if err := db.First(&client, id).Error; err != nil {
		t.Fatal(err)
	}
	if client.UpLoad != up || client.DownLoad != down {
		t.Fatalf("client traffic = %d/%d, want %d/%d", client.UpLoad, client.DownLoad, up, down)
	}
}

// Xray v26 prints counter values as JSON numbers; older builds as strings.
// Decoding numbers used to fail the whole response (no Xray accounting).
func TestParseXrayStatsNumericValues(t *testing.T) {
	raw := []byte(`{"stat":[
		{"name":"user>>>harvey>>>traffic>>>uplink","value":1976},
		{"name":"user>>>harvey>>>traffic>>>downlink","value":10522589},
		{"name":"user>>>old>>>traffic>>>downlink","value":"42"}
	]}`)
	got, err := parseXrayStats(raw)
	if err != nil {
		t.Fatalf("parseXrayStats: %v", err)
	}
	if got["harvey"].up != 1976 || got["harvey"].down != 10522589 {
		t.Errorf("harvey = %+v", got["harvey"])
	}
	if got["old"].down != 42 {
		t.Errorf("string-encoded value not parsed: %+v", got["old"])
	}
}
