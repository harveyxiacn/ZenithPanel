// Package deploy implements ZenithPanel's Smart Deploy: a preset-driven,
// idempotent, rollback-capable pipeline that turns a single user choice into
// a working egress tunnel (probe → preset expansion → cert → tuning → inbound
// → subscription).
//
// The contract is spelled out in docs/superpowers/specs/2026-04-21-smart-deploy-design.md.
// The implementation plan lives in docs/superpowers/plans/2026-04-21-phase1-smart-deploy.md.
//
// This file defines the in-memory types exchanged between probe, preset
// engine, cert manager, system tuner, and orchestrator. Persistence types
// (Deployment, DeploymentOp) live in internal/model/deploy.go.
package deploy

import "time"

// ─────────────────────────────────────────────────────────────────────────
// Probe
// ─────────────────────────────────────────────────────────────────────────

// ProbeResult is the aggregate output of the env probe. It is serialized as
// JSON into Deployment.ProbeSnapshot so the user can audit what the panel
// observed before it acted.
type ProbeResult struct {
	RootCheck    RootCheckResult `json:"root_check"`
	Kernel       KernelResult    `json:"kernel"`
	Systemd      SystemdResult   `json:"systemd"`
	Distro       DistroResult    `json:"distro"`
	TimeSync     TimeSyncResult  `json:"time_sync"`
	PublicIP     PublicIPResult  `json:"public_ip"`
	Hardware     HardwareResult  `json:"hardware"`
	NIC          NICResult       `json:"nic"`
	PortAvail    PortAvailResult `json:"port_avail"`
	InboundPorts []int           `json:"inbound_ports"`
	Firewall     FirewallResult  `json:"firewall"`
	Docker       DockerResult    `json:"docker"`
	ProbedAt     time.Time       `json:"probed_at"`
	DurationMs   int64           `json:"duration_ms"`
}

type RootCheckResult struct {
	OK   bool   `json:"ok"`
	UID  int    `json:"uid"`
	Note string `json:"note,omitempty"`
}

type KernelResult struct {
	Version  string         `json:"version"`
	Major    int            `json:"major"`
	Minor    int            `json:"minor"`
	Features KernelFeatures `json:"features"`
}

// KernelFeatures records which congestion-control and qdisc capabilities
// the running kernel exposes. Used by the preset engine and system tuner
// to pick values the kernel will actually accept.
type KernelFeatures struct {
	BBR     bool `json:"bbr"`
	FQ      bool `json:"fq"`
	FQCodel bool `json:"fq_codel"`
	Cake    bool `json:"cake"`
	TFO     bool `json:"tfo"`
}

type SystemdResult struct {
	Present bool   `json:"present"`
	Version string `json:"version,omitempty"`
}

// DistroResult is sourced from /etc/os-release. Unknown distros fall back to
// a generic path that avoids distro-specific package managers.
type DistroResult struct {
	ID         string `json:"id"` // debian | ubuntu | alpine | centos | rhel | fedora | unknown
	VersionID  string `json:"version_id"`
	PrettyName string `json:"pretty_name"`
}

type TimeSyncResult struct {
	Service string `json:"service"` // chronyd | systemd-timesyncd | ntpd | none
	Active  bool   `json:"active"`
	Synced  bool   `json:"synced"`
	Error   string `json:"error,omitempty"`
}

type PublicIPResult struct {
	V4    string `json:"v4"`
	V6    string `json:"v6,omitempty"`
	Error string `json:"error,omitempty"`
}

type HardwareResult struct {
	CPUCores  int   `json:"cpu_cores"`
	RAMBytes  int64 `json:"ram_bytes"`
	SwapBytes int64 `json:"swap_bytes"`
}

type NICResult struct {
	Primary       string `json:"primary"`
	LinkSpeedMbps int    `json:"link_speed_mbps"`
}

// PortAvailResult reports free/taken for a fixed set of probed ports. The
// exact port set is defined by the probe (443, 80, 8443 plus a sampled
// 10000-20000 range); callers should treat missing keys as "unknown."
type PortAvailResult struct {
	Ports map[int]bool `json:"ports"`
}

type FirewallResult struct {
	Type   string `json:"type"` // ufw | firewalld | nftables | iptables | none
	Active bool   `json:"active"`
}

type DockerResult struct {
	Present bool   `json:"present"`
	Running bool   `json:"running"`
	Version string `json:"version,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────
// Preset + Plan
// ─────────────────────────────────────────────────────────────────────────

// Input captures the user-supplied portion of a deploy request: the rest is
// derived from the probe + preset. All fields are optional; the preset
// engine fills in sane defaults when empty.
type Input struct {
	Domain        string         `json:"domain,omitempty"`
	Email         string         `json:"email,omitempty"`
	PortOverride  int            `json:"port_override,omitempty"`
	RealityTarget string         `json:"reality_target,omitempty"`
	Options       map[string]any `json:"options,omitempty"`
}

// DeployPlan is the expanded, concrete list of actions a preset will take
// when applied. It is persisted in Deployment.PlanSnapshot so that rollback
// and replay can operate on the exact plan that was executed.
type DeployPlan struct {
	PresetID          string        `json:"preset_id"`
	Inbounds          []InboundSpec `json:"inbounds"`
	Tuning            []TuneSpec    `json:"tuning"`
	CertMode          string        `json:"cert_mode"` // one of model.CertMode*
	CertInput         CertInput     `json:"cert_input,omitempty"`
	FirewallAllowPort []int         `json:"firewall_allow_ports,omitempty"`
	Notes             []string      `json:"notes,omitempty"` // user-facing heads-up (e.g. port fallback)
}

// InboundSpec is a protocol-engine-agnostic recipe for one inbound. The
// orchestrator translates it to the existing service/proxy Inbound model.
type InboundSpec struct {
	Engine   string         `json:"engine"` // xray | singbox
	Protocol string         `json:"protocol"`
	Tag      string         `json:"tag"`
	Listen   string         `json:"listen,omitempty"` // empty = dual-stack default
	Port     int            `json:"port"`
	Network  string         `json:"network,omitempty"` // tcp | udp | ws | grpc | ...
	Settings map[string]any `json:"settings"`
	Stream   map[string]any `json:"stream,omitempty"`
	Remark   string         `json:"remark,omitempty"`
}

// TuneSpec names a reversible system-tuning operation plus its parameters.
// The system tuner package owns the catalog of op names and what params mean.
type TuneSpec struct {
	OpName string            `json:"op_name"`
	Params map[string]string `json:"params,omitempty"`
}

// CertInput carries the user's cert choices into the cert manager. Which
// fields matter depends on DeployPlan.CertMode:
//   - reality: none of these are needed
//   - acme: Domain, Email
//   - self_signed: PublicIP (for SAN)
//   - existing: CertPath, KeyPath
type CertInput struct {
	Domain   string `json:"domain,omitempty"`
	Email    string `json:"email,omitempty"`
	PublicIP string `json:"public_ip,omitempty"`
	CertPath string `json:"cert_path,omitempty"`
	KeyPath  string `json:"key_path,omitempty"`
}

// Preset is a compile-time-registered smart-deploy recipe. The Expander
// function is deterministic given the same probe + input (modulo the random
// secrets it generates via crypto/rand for UUIDs, short-ids, and passwords).
type Preset struct {
	ID          string                                                `json:"id"`
	DisplayName map[string]string                                     `json:"display_name"` // locale -> display
	Description map[string]string                                     `json:"description"`
	Recommended bool                                                  `json:"recommended"`
	Expander    func(probe ProbeResult, in Input) (DeployPlan, error) `json:"-"`
}
