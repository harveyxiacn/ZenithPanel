package deploy

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/system"
	"gorm.io/gorm"
)

// setupTestOrchestrator builds an Orchestrator with an ephemeral in-memory
// SQLite DB and fully stubbed dependencies. Returns the orchestrator plus
// the stubs so tests can inspect their state.
func setupTestOrchestrator(t *testing.T) (*Orchestrator, *gorm.DB, *stubTuner, *stubInbounds, *stubCerts) {
	t.Helper()
	// file::memory:?cache=shared is the in-memory form the glebarez driver
	// handles; :memory: alone works per-connection and is fine for a single
	// orchestrator instance in tests.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := db.AutoMigrate(&model.Inbound{}, &model.Deployment{}, &model.DeploymentOp{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tuner := &stubTuner{}
	inbounds := &stubInbounds{db: db}
	certs := &stubCerts{}

	probe := NewWithRunner(&fakeRunner{
		uid: 0,
		files: map[string][]byte{
			"/proc/version": []byte("Linux version 5.15.0"),
		},
		portFreeSet:  map[int]bool{443: true, 8443: true},
		ipv4:         "1.2.3.4",
		cpuCores:     2,
		ramBytes:     2 << 30,
		nicName:      "eth0",
		nicSpeedMbps: 1000,
	})

	o := NewTestOrchestrator(db, probe, certs, tuner, inbounds)
	return o, db, tuner, inbounds, certs
}

// stubTuner records calls and optionally fails a specific op name.
type stubTuner struct {
	applied  []string
	reverted []string
	failOn   string
}

func (s *stubTuner) Apply(_ context.Context, name string, _ map[string]string) (system.Snapshot, error) {
	if name == s.failOn {
		return system.Snapshot{}, errors.New("forced failure")
	}
	s.applied = append(s.applied, name)
	return system.Snapshot{Op: name}, nil
}

func (s *stubTuner) Revert(_ context.Context, snap system.Snapshot) error {
	s.reverted = append(s.reverted, snap.Op)
	return nil
}

type stubInbounds struct {
	db      *gorm.DB
	created []string
	deleted []uint
	failOn  string
}

func (s *stubInbounds) Create(spec InboundSpec) (uint, error) {
	if spec.Protocol == s.failOn {
		return 0, errors.New("forced inbound failure")
	}
	s.created = append(s.created, spec.Tag)
	// Actually write to DB so the deployment's inbound_ids reflect real rows.
	ib := model.Inbound{Tag: spec.Tag, Protocol: spec.Protocol, Port: spec.Port, Settings: "{}", Network: spec.Network}
	if err := s.db.Create(&ib).Error; err != nil {
		return 0, err
	}
	return ib.ID, nil
}

func (s *stubInbounds) Delete(id uint) error {
	s.deleted = append(s.deleted, id)
	return s.db.Delete(&model.Inbound{}, id).Error
}

type stubCerts struct {
	failWith error
}

func (s *stubCerts) Provision(_ context.Context, in cert.ProvisionInput) (*cert.ProvisionResult, error) {
	if s.failWith != nil {
		return nil, s.failWith
	}
	if in.Mode == cert.ModeReality {
		return &cert.ProvisionResult{Mode: cert.ModeReality}, nil
	}
	return &cert.ProvisionResult{Mode: in.Mode, CertPath: "/tmp/c.pem", KeyPath: "/tmp/k.pem"}, nil
}

// ─────────────────────────────────────────────────────────────────────────

func TestApplyStableEgressHappyPath(t *testing.T) {
	o, db, tuner, inbounds, _ := setupTestOrchestrator(t)

	dep, err := o.Apply(context.Background(), model.PresetStableEgress, Input{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if dep.Status != model.DeployStatusSucceeded {
		t.Errorf("Status = %q, want succeeded", dep.Status)
	}
	if len(inbounds.created) != 1 {
		t.Errorf("created inbounds = %d, want 1", len(inbounds.created))
	}
	if len(tuner.applied) == 0 {
		t.Errorf("no tuning ops applied")
	}

	// The ops ledger should contain cert + tuning + inbound rows, all applied.
	var ops []model.DeploymentOp
	db.Where("deployment_id = ?", dep.ID).Order("sequence").Find(&ops)
	if len(ops) == 0 {
		t.Fatalf("no ops recorded")
	}
	for _, op := range ops {
		if op.Status != model.OpStatusApplied {
			t.Errorf("op %s status = %q, want applied", op.OpName, op.Status)
		}
	}
}

func TestApplyRollsBackOnInboundFailure(t *testing.T) {
	o, db, tuner, inbounds, _ := setupTestOrchestrator(t)
	inbounds.failOn = "vless" // fail the VLESS inbound creation

	dep, err := o.Apply(context.Background(), model.PresetStableEgress, Input{})
	if err == nil {
		t.Fatalf("expected Apply to return error")
	}
	if dep.Status != model.DeployStatusFailed {
		t.Errorf("Status = %q, want failed", dep.Status)
	}
	// Every applied tune op should have been reverted.
	if len(tuner.reverted) != len(tuner.applied) {
		t.Errorf("applied=%d reverted=%d — rollback didn't walk back all ops",
			len(tuner.applied), len(tuner.reverted))
	}
	// Op statuses reflect the rollback.
	var ops []model.DeploymentOp
	db.Where("deployment_id = ?", dep.ID).Find(&ops)
	hasReverted := false
	for _, op := range ops {
		if op.Status == model.OpStatusReverted {
			hasReverted = true
		}
	}
	if !hasReverted {
		t.Errorf("no ops were marked reverted")
	}
}

func TestApplyRefusesWhenNotRoot(t *testing.T) {
	o, _, _, _, _ := setupTestOrchestrator(t)
	o.probe = NewWithRunner(&fakeRunner{uid: 1000, files: map[string][]byte{"/proc/version": []byte("Linux 5.15")}})

	_, err := o.Apply(context.Background(), model.PresetStableEgress, Input{})
	if err == nil {
		t.Fatalf("expected refusal when not root")
	}
}

func TestApplyStoresInboundIDsOnDeployment(t *testing.T) {
	o, db, _, _, _ := setupTestOrchestrator(t)
	dep, err := o.Apply(context.Background(), model.PresetStableEgress, Input{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	var reloaded model.Deployment
	db.First(&reloaded, dep.ID)
	if reloaded.InboundIDs == "" || reloaded.InboundIDs == "[]" {
		t.Errorf("InboundIDs = %q, expected non-empty array", reloaded.InboundIDs)
	}
}

func TestRollbackExplicit(t *testing.T) {
	o, db, tuner, inbounds, _ := setupTestOrchestrator(t)

	dep, err := o.Apply(context.Background(), model.PresetStableEgress, Input{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	appliedCount := len(tuner.applied)
	createdCount := len(inbounds.created)

	if _, err := o.Rollback(context.Background(), dep.ID); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	if len(tuner.reverted) != appliedCount {
		t.Errorf("reverted=%d applied=%d", len(tuner.reverted), appliedCount)
	}
	if len(inbounds.deleted) != createdCount {
		t.Errorf("deleted=%d created=%d", len(inbounds.deleted), createdCount)
	}
	var reloaded model.Deployment
	db.First(&reloaded, dep.ID)
	if reloaded.Status != model.DeployStatusRolledBack {
		t.Errorf("status=%q, want rolled_back", reloaded.Status)
	}
}

func TestRollbackIsIdempotent(t *testing.T) {
	o, _, _, _, _ := setupTestOrchestrator(t)
	dep, _ := o.Apply(context.Background(), model.PresetStableEgress, Input{})

	// First rollback
	if _, err := o.Rollback(context.Background(), dep.ID); err != nil {
		t.Fatalf("Rollback 1: %v", err)
	}
	// Second rollback should be a no-op and return no error.
	if _, err := o.Rollback(context.Background(), dep.ID); err != nil {
		t.Fatalf("Rollback 2: %v", err)
	}
}

func TestPreviewDoesNotApplyOps(t *testing.T) {
	o, db, tuner, inbounds, _ := setupTestOrchestrator(t)
	plan, _, err := o.Preview(context.Background(), model.PresetStableEgress, Input{})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if plan.PresetID != model.PresetStableEgress {
		t.Errorf("plan PresetID = %q", plan.PresetID)
	}
	// No side effects: no deployments, no tuner calls, no inbounds.
	var count int64
	db.Model(&model.Deployment{}).Count(&count)
	if count != 0 {
		t.Errorf("deployment rows after preview = %d, want 0", count)
	}
	if len(tuner.applied) != 0 || len(inbounds.created) != 0 {
		t.Errorf("preview mutated state: tuner=%v inbounds=%v", tuner.applied, inbounds.created)
	}
}

func TestApplyReturnsCertFailureAsDeployFailure(t *testing.T) {
	o, _, _, _, certs := setupTestOrchestrator(t)
	certs.failWith = errors.New("simulated cert failure")

	// speed preset requires a cert (not reality), so cert failure propagates.
	dep, err := o.Apply(context.Background(), model.PresetSpeed, Input{})
	if err == nil {
		t.Fatalf("expected cert failure to surface")
	}
	if dep.Status != model.DeployStatusFailed {
		t.Errorf("Status = %q, want failed", dep.Status)
	}
}

// ensure TempDir cleanup works on Windows (gorm/sqlite keeps handles open).
func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
