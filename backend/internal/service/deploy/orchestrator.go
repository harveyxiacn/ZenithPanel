package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/cert"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/system"
	"gorm.io/gorm"
)

// Orchestrator drives a Smart Deploy from probe through plan application.
// It keeps a DeploymentOp row per side-effect so rollback and idempotent
// re-apply both operate on the same durable log.
type Orchestrator struct {
	db       *gorm.DB
	probe    *Probe
	certs    CertProvisioner
	tuner    Tuner
	inbounds InboundDeployer

	// clock is injectable so tests can freeze time. Nil falls back to
	// time.Now.
	clock func() time.Time
}

// CertProvisioner is the narrow interface the orchestrator needs from the
// cert package. Satisfied by *cert.Manager in production and by stubs in
// tests.
type CertProvisioner interface {
	Provision(ctx context.Context, in cert.ProvisionInput) (*cert.ProvisionResult, error)
}

// Tuner is the narrow interface the orchestrator needs from the system tuner.
// Satisfied by the package-level system.ApplyTuneOp / system.RevertTuneOp
// via tuneAdapter in production; tests provide a stub.
type Tuner interface {
	Apply(ctx context.Context, opName string, params map[string]string) (system.Snapshot, error)
	Revert(ctx context.Context, snap system.Snapshot) error
}

// InboundDeployer is the narrow interface the orchestrator needs to create
// and remove inbounds. The production implementation writes directly to
// the same inbounds table the manual UI uses; tests can stub.
type InboundDeployer interface {
	Create(spec InboundSpec) (inboundID uint, err error)
	Delete(inboundID uint) error
}

// NewOrchestrator wires production implementations. Callers pass the
// shared DB and the cert manager; the tuner and inbound deployer default
// to adapters over the respective packages.
func NewOrchestrator(db *gorm.DB, certs CertProvisioner) *Orchestrator {
	return &Orchestrator{
		db:       db,
		probe:    New(db),
		certs:    certs,
		tuner:    tuneAdapter{},
		inbounds: dbInboundDeployer{db: db},
		clock:    time.Now,
	}
}

// NewTestOrchestrator lets tests inject every dependency.
func NewTestOrchestrator(db *gorm.DB, p *Probe, certs CertProvisioner, t Tuner, i InboundDeployer) *Orchestrator {
	return &Orchestrator{db: db, probe: p, certs: certs, tuner: t, inbounds: i, clock: time.Now}
}

// Probe runs the environment probe and returns the result without writing
// to the DB.
func (o *Orchestrator) Probe(ctx context.Context) ProbeResult {
	return o.probe.Run(ctx)
}

// Preview expands a preset against a fresh probe, returning the plan
// without executing it.
func (o *Orchestrator) Preview(ctx context.Context, presetID string, in Input) (DeployPlan, ProbeResult, error) {
	pr := o.probe.Run(ctx)
	plan, err := Expand(presetID, pr, in)
	return plan, pr, err
}

// Apply executes a Smart Deploy. It records a Deployment row, then walks
// each op (cert → tuning → inbounds → firewall), writing a DeploymentOp
// row before each side-effect. A mid-pipeline failure triggers automatic
// rollback of already-applied ops.
func (o *Orchestrator) Apply(ctx context.Context, presetID string, in Input) (*model.Deployment, error) {
	pr := o.probe.Run(ctx)
	plan, err := Expand(presetID, pr, in)
	if err != nil {
		return nil, err
	}
	if !pr.RootCheck.OK {
		return nil, errors.New("deploy refused: panel is not running as root")
	}

	dep, err := o.createDeploymentRecord(presetID, pr, plan, in)
	if err != nil {
		return nil, err
	}

	if err := o.execute(ctx, dep, plan); err != nil {
		o.rollbackOps(ctx, dep.ID)
		o.markDeployment(dep, model.DeployStatusFailed, err)
		return dep, err
	}

	o.markDeployment(dep, model.DeployStatusSucceeded, nil)
	return dep, nil
}

// Rollback reverts every applied op of an existing deployment in reverse
// order and marks the deployment as rolled_back. Idempotent: calling it
// twice on the same deployment is a no-op after the first run.
func (o *Orchestrator) Rollback(ctx context.Context, id uint) (*model.Deployment, error) {
	var dep model.Deployment
	if err := o.db.First(&dep, id).Error; err != nil {
		return nil, err
	}
	if dep.Status == model.DeployStatusRolledBack {
		return &dep, nil
	}

	o.rollbackOps(ctx, id)
	o.markDeployment(&dep, model.DeployStatusRolledBack, nil)
	return &dep, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Pipeline
// ─────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) execute(ctx context.Context, dep *model.Deployment, plan DeployPlan) error {
	seq := 0

	// 1. Cert provisioning (first so inbound stream paths can be filled in).
	certRes, err := o.runCert(ctx, dep, plan, &seq)
	if err != nil {
		return fmt.Errorf("cert: %w", err)
	}

	// 2. Tuning ops.
	for _, ts := range plan.Tuning {
		if err := o.runTune(ctx, dep, ts, &seq); err != nil {
			return fmt.Errorf("tune %q: %w", ts.OpName, err)
		}
	}

	// 3. Inbounds (after cert so paths can be injected into stream settings).
	inboundIDs := []uint{}
	for _, spec := range plan.Inbounds {
		injected := injectCertPaths(spec, certRes)
		id, err := o.runInbound(ctx, dep, injected, &seq)
		if err != nil {
			return fmt.Errorf("inbound %q: %w", spec.Tag, err)
		}
		inboundIDs = append(inboundIDs, id)
	}

	// Persist the inbound IDs on the deployment for quick lookup.
	if b, err := json.Marshal(inboundIDs); err == nil {
		o.db.Model(dep).Update("inbound_ids", string(b))
	}
	return nil
}

func (o *Orchestrator) runCert(ctx context.Context, dep *model.Deployment, plan DeployPlan, seq *int) (*cert.ProvisionResult, error) {
	input := cert.ProvisionInput{
		Mode:     cert.Mode(plan.CertMode),
		Domain:   plan.CertInput.Domain,
		Email:    plan.CertInput.Email,
		PublicIP: plan.CertInput.PublicIP,
		CertPath: plan.CertInput.CertPath,
		KeyPath:  plan.CertInput.KeyPath,
	}
	preJSON, _ := json.Marshal(input)
	op := &model.DeploymentOp{
		DeploymentID: dep.ID,
		Sequence:     *seq,
		OpType:       model.OpTypeCert,
		OpName:       "cert.provision",
		PreValue:     string(preJSON),
		Status:       model.OpStatusPending,
	}
	*seq++
	if err := o.db.Create(op).Error; err != nil {
		return nil, err
	}
	res, err := o.certs.Provision(ctx, input)
	if err != nil {
		o.failOp(op, err)
		return nil, err
	}
	postJSON, _ := json.Marshal(res)
	op.PostValue = string(postJSON)
	op.Status = model.OpStatusApplied
	op.AppliedAt = o.clock()
	o.db.Save(op)
	return res, nil
}

func (o *Orchestrator) runTune(ctx context.Context, dep *model.Deployment, ts TuneSpec, seq *int) error {
	preJSON, _ := json.Marshal(ts)
	op := &model.DeploymentOp{
		DeploymentID: dep.ID,
		Sequence:     *seq,
		OpType:       model.OpTypeTune,
		OpName:       ts.OpName,
		PreValue:     string(preJSON),
		Status:       model.OpStatusPending,
	}
	*seq++
	if err := o.db.Create(op).Error; err != nil {
		return err
	}
	snap, err := o.tuner.Apply(ctx, ts.OpName, ts.Params)
	if err != nil {
		o.failOp(op, err)
		return err
	}
	postJSON, _ := json.Marshal(snap)
	op.PostValue = string(postJSON)
	op.Status = model.OpStatusApplied
	op.AppliedAt = o.clock()
	o.db.Save(op)
	return nil
}

func (o *Orchestrator) runInbound(_ context.Context, dep *model.Deployment, spec InboundSpec, seq *int) (uint, error) {
	preJSON, _ := json.Marshal(spec)
	op := &model.DeploymentOp{
		DeploymentID: dep.ID,
		Sequence:     *seq,
		OpType:       model.OpTypeInbound,
		OpName:       "inbound." + spec.Protocol,
		PreValue:     string(preJSON),
		Status:       model.OpStatusPending,
	}
	*seq++
	if err := o.db.Create(op).Error; err != nil {
		return 0, err
	}
	id, err := o.inbounds.Create(spec)
	if err != nil {
		o.failOp(op, err)
		return 0, err
	}
	op.PostValue = fmt.Sprintf(`{"inbound_id":%d}`, id)
	op.Status = model.OpStatusApplied
	op.AppliedAt = o.clock()
	o.db.Save(op)
	return id, nil
}

// rollbackOps reverts every applied op for a deployment in reverse order.
// Errors on individual ops are logged into that op's Error field but do not
// abort the rollback — we always try to revert as much as possible.
func (o *Orchestrator) rollbackOps(ctx context.Context, depID uint) {
	var ops []model.DeploymentOp
	o.db.Where("deployment_id = ? AND status = ?", depID, model.OpStatusApplied).
		Order("sequence DESC").
		Find(&ops)

	for i := range ops {
		op := &ops[i]
		if err := o.revertOne(ctx, op); err != nil {
			op.Error = err.Error()
			op.Status = model.OpStatusFailed
		} else {
			op.Status = model.OpStatusReverted
		}
		o.db.Save(op)
	}
}

func (o *Orchestrator) revertOne(ctx context.Context, op *model.DeploymentOp) error {
	switch op.OpType {
	case model.OpTypeCert:
		// Certs are not revoked — keeping them allows the user to retry
		// without another ACME round-trip. Self-signed files stay on disk.
		return nil
	case model.OpTypeTune:
		var snap system.Snapshot
		if err := json.Unmarshal([]byte(op.PostValue), &snap); err != nil {
			return err
		}
		return o.tuner.Revert(ctx, snap)
	case model.OpTypeInbound:
		var post struct {
			InboundID uint `json:"inbound_id"`
		}
		if err := json.Unmarshal([]byte(op.PostValue), &post); err != nil {
			return err
		}
		if post.InboundID == 0 {
			return nil
		}
		return o.inbounds.Delete(post.InboundID)
	default:
		return nil
	}
}

// ─────────────────────────────────────────────────────────────────────────
// DB helpers
// ─────────────────────────────────────────────────────────────────────────

func (o *Orchestrator) createDeploymentRecord(presetID string, pr ProbeResult, plan DeployPlan, in Input) (*model.Deployment, error) {
	probeJSON, _ := json.Marshal(pr)
	planJSON, _ := json.Marshal(plan)
	dep := &model.Deployment{
		PresetID:      presetID,
		Status:        model.DeployStatusRunning,
		ProbeSnapshot: string(probeJSON),
		PlanSnapshot:  string(planJSON),
		Domain:        in.Domain,
		CertMode:      plan.CertMode,
		InboundIDs:    "[]",
	}
	return dep, o.db.Create(dep).Error
}

func (o *Orchestrator) markDeployment(dep *model.Deployment, status string, err error) {
	dep.Status = status
	if err != nil {
		dep.Error = err.Error()
	}
	o.db.Save(dep)
}

func (o *Orchestrator) failOp(op *model.DeploymentOp, err error) {
	op.Error = err.Error()
	op.Status = model.OpStatusFailed
	o.db.Save(op)
}

// ─────────────────────────────────────────────────────────────────────────
// Production adapters
// ─────────────────────────────────────────────────────────────────────────

// tuneAdapter routes through the system package's package-level registry.
type tuneAdapter struct{}

func (tuneAdapter) Apply(ctx context.Context, opName string, params map[string]string) (system.Snapshot, error) {
	return system.ApplyTuneOp(ctx, opName, params)
}

func (tuneAdapter) Revert(ctx context.Context, snap system.Snapshot) error {
	return system.RevertTuneOp(ctx, snap)
}

// dbInboundDeployer creates/deletes rows in the existing inbounds table.
// The running proxy core is expected to pick up the new rows via its usual
// watch/reload path; orchestrator does not poke the core directly.
type dbInboundDeployer struct {
	db *gorm.DB
}

func (d dbInboundDeployer) Create(spec InboundSpec) (uint, error) {
	settingsJSON, err := json.Marshal(spec.Settings)
	if err != nil {
		return 0, err
	}
	streamJSON, err := json.Marshal(spec.Stream)
	if err != nil {
		return 0, err
	}
	network := spec.Network
	if network == "" {
		network = "tcp"
	}
	ib := model.Inbound{
		Tag:      spec.Tag,
		Protocol: spec.Protocol,
		Listen:   spec.Listen,
		Port:     spec.Port,
		Network:  network,
		Settings: string(settingsJSON),
		Stream:   string(streamJSON),
		Enable:   true,
		Remark:   spec.Remark,
	}
	if err := d.db.Create(&ib).Error; err != nil {
		return 0, err
	}
	return ib.ID, nil
}

func (d dbInboundDeployer) Delete(id uint) error {
	return d.db.Delete(&model.Inbound{}, id).Error
}

// injectCertPaths fills in cert_path / key_path for Hy2/TUIC TLS streams
// once the CertManager has produced them. Reality streams are untouched.
func injectCertPaths(spec InboundSpec, res *cert.ProvisionResult) InboundSpec {
	if res == nil || res.CertPath == "" {
		return spec
	}
	tls, ok := spec.Stream["tls"].(map[string]any)
	if !ok {
		return spec
	}
	tls["certificate_path"] = res.CertPath
	tls["key_path"] = res.KeyPath
	if res.SelfSigned {
		tls["insecure"] = true
	}
	return spec
}
