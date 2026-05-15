package model

import (
	"time"

	"gorm.io/gorm"
)

// Preset IDs for Smart Deploy. Mirrored in frontend/src/types/deploy.ts.
const (
	PresetStableEgress = "stable_egress"
	PresetSpeed        = "speed"
	PresetCombo        = "combo"
	PresetWeakNetwork  = "weak_network"
)

// Deployment lifecycle statuses.
const (
	DeployStatusPending    = "pending"
	DeployStatusRunning    = "running"
	DeployStatusSucceeded  = "succeeded"
	DeployStatusFailed     = "failed"
	DeployStatusRolledBack = "rolled_back"
)

// Cert provisioning modes for a deployment.
const (
	CertModeReality    = "reality"     // Reality: no server cert needed
	CertModeACME       = "acme"        // ACME-issued cert (lego)
	CertModeSelfSigned = "self_signed" // locally generated self-signed cert
	CertModeExisting   = "existing"    // user-provided cert + key paths
)

// Per-op status within a deployment.
const (
	OpStatusPending  = "pending"
	OpStatusApplied  = "applied"
	OpStatusSkipped  = "skipped" // idempotent: current already matches target
	OpStatusFailed   = "failed"
	OpStatusReverted = "reverted"
)

// Op types cover what a DeploymentOp represents.
const (
	OpTypeProbe    = "probe"
	OpTypeTune     = "tune"
	OpTypeCert     = "cert"
	OpTypeInbound  = "inbound"
	OpTypeFirewall = "firewall"
)

// Deployment records a single smart-deploy run.
//
// JSON-shaped fields are stored as text blobs (matching the project's existing
// pattern in model/proxy.go) rather than datatypes.JSON to avoid pulling in a
// new dependency. Callers marshal/unmarshal at the service boundary.
type Deployment struct {
	ID            uint   `gorm:"primaryKey" json:"id"`
	PresetID      string `gorm:"not null;index" json:"preset_id"`
	Status        string `gorm:"not null;index;default:'pending'" json:"status"`
	ProbeSnapshot string `gorm:"type:text" json:"probe_snapshot"` // JSON of service/deploy.ProbeResult
	PlanSnapshot  string `gorm:"type:text" json:"plan_snapshot"`  // JSON of service/deploy.DeployPlan
	Domain        string `gorm:"default:''" json:"domain"`
	CertMode      string `gorm:"default:''" json:"cert_mode"`
	InboundIDs    string `gorm:"type:text;default:'[]'" json:"inbound_ids"` // JSON array of uint
	Error         string `gorm:"type:text;default:''" json:"error"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// DeploymentOp records a single reversible step within a Deployment, enabling
// snapshot-based rollback. Ops are written before their side effects so that a
// crash mid-apply still leaves a breadcrumb for cleanup.
type DeploymentOp struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	DeploymentID uint      `gorm:"not null;index" json:"deployment_id"`
	Sequence     int       `gorm:"not null" json:"sequence"`
	OpType       string    `gorm:"not null" json:"op_type"`
	OpName       string    `gorm:"not null" json:"op_name"`
	PreValue     string    `gorm:"type:text" json:"pre_value"`  // JSON snapshot before the op
	PostValue    string    `gorm:"type:text" json:"post_value"` // JSON target state after the op
	Status       string    `gorm:"not null;default:'pending'" json:"status"`
	Error        string    `gorm:"type:text;default:''" json:"error"`
	AppliedAt    time.Time `json:"applied_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
