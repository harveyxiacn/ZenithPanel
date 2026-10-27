package proxy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"gorm.io/gorm"
)

type fakeRuntimeEngine struct {
	value                            engineSnapshot
	candidate                        string
	prepareErr, startErr, restoreErr error
	stops, starts                    int
}

func (f *fakeRuntimeEngine) snapshot() (engineSnapshot, error) { return f.value, nil }
func (f *fakeRuntimeEngine) prepare(context.Context, bool) (string, error) {
	return f.candidate, f.prepareErr
}
func (f *fakeRuntimeEngine) stop() error { f.stops++; f.value.running = false; return nil }
func (f *fakeRuntimeEngine) start(value string, dual bool) error {
	f.starts++
	f.value = engineSnapshot{config: value, exists: true, running: f.startErr == nil, dual: dual}
	return f.startErr
}
func (f *fakeRuntimeEngine) restore(old engineSnapshot) error {
	if f.restoreErr != nil {
		return f.restoreErr
	}
	f.value = old
	return nil
}
func runtimeFixture() (*fakeRuntimeEngine, *fakeRuntimeEngine, []runtimeTarget) {
	x := &fakeRuntimeEngine{value: engineSnapshot{config: "old-x", exists: true, running: true, dual: true}, candidate: "new-x"}
	s := &fakeRuntimeEngine{value: engineSnapshot{config: "old-s", exists: true, running: true, dual: true}, candidate: "new-s"}
	return x, s, []runtimeTarget{{name: "xray", engine: x, wanted: true, dual: true}, {name: "singbox", engine: s, wanted: true, dual: true}}
}

func TestRuntimePreflightPreservesBothLiveEngines(t *testing.T) {
	x, s, targets := runtimeFixture()
	s.prepareErr = errors.New("invalid TLS certificate")
	_, restored, err := transitionRuntime(context.Background(), targets, nil)
	if err == nil || restored || x.stops != 0 || s.stops != 0 || !x.value.running || !s.value.running {
		t.Fatalf("preflight disturbed live engines: err=%v restored=%v x=%+v s=%+v", err, restored, x, s)
	}
}

func TestRuntimeStartFailureRestoresPreviousBytesAndMode(t *testing.T) {
	x, s, targets := runtimeFixture()
	x.value.dual = false
	s.startErr = errors.New("port busy")
	_, restored, err := transitionRuntime(context.Background(), targets, nil)
	if err == nil || !restored || x.value.config != "old-x" || s.value.config != "old-s" || x.value.dual || !x.value.running || !s.value.running {
		t.Fatalf("rollback failed: err=%v restored=%v x=%+v s=%+v", err, restored, x, s)
	}
}

func TestRuntimeProbeFailureRestoresBothEngines(t *testing.T) {
	x, s, targets := runtimeFixture()
	_, restored, err := transitionRuntime(context.Background(), targets, func() error { return errors.New("inbound not bound") })
	if err == nil || !restored || x.value.config != "old-x" || s.value.config != "old-s" {
		t.Fatalf("bad restore: %v %v", restored, err)
	}
}

func TestRuntimeReportsRollbackFailure(t *testing.T) {
	x, s, targets := runtimeFixture()
	s.startErr = errors.New("candidate failed")
	x.restoreErr = errors.New("old engine cannot restart")
	_, restored, err := transitionRuntime(context.Background(), targets, nil)
	if restored || err == nil || !strings.Contains(err.Error(), "old engine cannot restart") {
		t.Fatalf("hidden rollback failure: %v %v", restored, err)
	}
}

func TestRuntimeCommitAndSingleEngineSelection(t *testing.T) {
	x, s, targets := runtimeFixture()
	targets[0].wanted = false
	targets[1].dual = false
	revision, restored, err := transitionRuntime(context.Background(), targets, nil)
	if err != nil || restored || len(revision) != 64 || x.value.running || !s.value.running || s.value.dual || s.value.config != "new-s" {
		t.Fatalf("bad commit: %s %v %+v %+v", revision, err, x, s)
	}
}

func TestRuntimeCancellationBeforeSwitchDoesNotStopEngines(t *testing.T) {
	x, s, targets := runtimeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := transitionRuntime(ctx, targets, nil)
	if !errors.Is(err, context.Canceled) || x.stops != 0 || s.stops != 0 {
		t.Fatalf("cancelled operation changed runtime: %v", err)
	}
}

func TestRuntimeGateWaitCanBeCancelled(t *testing.T) {
	runtimeGate <- struct{}{}
	defer func() { <-runtimeGate }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ApplyRuntime(ctx, NewXrayManager(), NewSingboxManager(), "auto")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestPersistExternalRuntimeRevisionIsAtomic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:runtime-persist-atomic?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]model.Setting{
		{Key: runtimeModeSetting, Value: "auto"},
		{Key: "last_apply_unix", Value: "100"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Fail the second insert after the mode upsert has succeeded. SQLite rolls
	// the enclosing transaction back, exposing the old state to OTA readiness.
	if err := db.Exec(`CREATE TRIGGER reject_external_revision BEFORE INSERT ON settings
		WHEN NEW.key = 'external_runtime_revision'
		BEGIN SELECT RAISE(ABORT, 'injected revision write failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	oldDB := config.DB
	config.DB = db
	t.Cleanup(func() { config.DB = oldDB })

	err = persistExternalRuntimeRevision(context.Background(), "xray", "new-revision")
	if err == nil || !strings.Contains(err.Error(), "injected revision write failure") {
		t.Fatalf("expected injected transaction failure, got %v", err)
	}
	var settings []model.Setting
	if err := db.Order("key").Find(&settings).Error; err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, setting := range settings {
		got[setting.Key] = setting.Value
	}
	if got[runtimeModeSetting] != "auto" || got["last_apply_unix"] != "100" {
		t.Fatalf("transaction partially persisted settings: %#v", got)
	}
	if _, exists := got[externalRevisionSetting]; exists {
		t.Fatalf("revision persisted despite failed transaction: %#v", got)
	}
}

func TestRuntimeUnchangedEngineSurvivesOtherEngineRollback(t *testing.T) {
	x, s, targets := runtimeFixture()
	x.candidate = x.value.config
	s.startErr = errors.New("port busy")
	_, restored, err := transitionRuntime(context.Background(), targets, nil)
	if err == nil || !restored || x.stops != 0 || x.starts != 0 || !x.value.running {
		t.Fatalf("unchanged Xray was interrupted: err=%v restored=%v x=%+v", err, restored, x)
	}
}

func TestRuntimeNoChangeSkipsRestartUnlessForced(t *testing.T) {
	for _, force := range []bool{false, true} {
		x, s, targets := runtimeFixture()
		x.candidate, s.candidate = x.value.config, s.value.config
		targets[0].force, targets[1].force = force, force
		_, _, err := transitionRuntime(context.Background(), targets, nil)
		want := 0
		if force {
			want = 1
		}
		if err != nil || x.stops != want || s.stops != want || x.starts != want || s.starts != want {
			t.Fatalf("force=%v err=%v x=%+v s=%+v", force, err, x, s)
		}
	}
}
