package deploy

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// defaultRealityTarget is the SNI Reality borrows when the user doesn't
// override it. Microsoft's landing page is a stable, globally-reachable,
// high-volume TLS endpoint — exactly the kind of traffic risk engines
// treat as unremarkable.
const defaultRealityTarget = "www.microsoft.com"

// portFallbacks is the ordered list the preset engine walks when a
// preferred port is taken in the probe snapshot.
var portFallbacks = []int{443, 8443, 2053, 2083, 2087, 2096, 10443, 20443}

// tuning op names used in TuneSpec.OpName. The system tuner package owns
// the catalog; keeping the identifiers centralized here avoids drift.
const (
	TuneBBRFQ          = "bbr_fq"
	TuneSysctlNetwork  = "sysctl_network"
	TuneUDPBuffersLarge = "udp_buffers_large"
	TuneTCPFastOpenFull = "tcp_fastopen_full"
	TuneSystemdNofile   = "systemd_nofile"
	TuneTimeSyncEnable  = "time_sync_enable"
	TuneQdiscCake       = "qdisc_cake"
)

// Expand turns a preset ID + environment snapshot + user input into a
// concrete DeployPlan. Non-secret fields are deterministic in inputs;
// secret fields (UUIDs, Reality keys, passwords) are generated via
// crypto/rand so identical inputs still produce distinct plans.
func Expand(presetID string, probe ProbeResult, in Input) (DeployPlan, error) {
	switch presetID {
	case model.PresetStableEgress:
		return expandStableEgress(probe, in)
	case model.PresetSpeed:
		return expandSpeed(probe, in)
	case model.PresetCombo:
		return expandCombo(probe, in)
	case model.PresetWeakNetwork:
		return expandWeakNetwork(probe, in)
	default:
		return DeployPlan{}, fmt.Errorf("unknown preset: %q", presetID)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Preset expanders
// ─────────────────────────────────────────────────────────────────────────

func expandStableEgress(probe ProbeResult, in Input) (DeployPlan, error) {
	port, note := pickPort(443, in.PortOverride, probe, "tcp")

	realityTarget := in.RealityTarget
	if realityTarget == "" {
		realityTarget = defaultRealityTarget
	}

	priv, pub, err := generateX25519Keys()
	if err != nil {
		return DeployPlan{}, fmt.Errorf("generate reality keys: %w", err)
	}
	shortID, err := randomHex(8)
	if err != nil {
		return DeployPlan{}, err
	}
	clientID := uuid.NewString()

	inbound := InboundSpec{
		Engine:   "xray",
		Protocol: "vless",
		Tag:      "smart-stable-egress-" + shortID[:6],
		Port:     port,
		Network:  "tcp",
		Settings: map[string]any{
			"decryption": "none",
			"clients": []map[string]any{
				{
					"id":    clientID,
					"flow":  "xtls-rprx-vision",
					"email": "default",
				},
			},
		},
		Stream: map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"show":          false,
				"dest":          realityTarget + ":443",
				"serverNames":   []string{realityTarget},
				"privateKey":    priv,
				"publicKey":     pub,
				"shortIds":      []string{shortID},
				"fingerprint":   "chrome",
				"minClientVer":  "1.8.0",
				"maxTimeDiff":   70,
			},
		},
		Remark: "Smart Deploy — stable egress (Reality)",
	}

	plan := DeployPlan{
		PresetID: model.PresetStableEgress,
		Inbounds: []InboundSpec{inbound},
		Tuning: []TuneSpec{
			{OpName: TuneBBRFQ},
			{OpName: TuneSysctlNetwork},
			{OpName: TuneTCPFastOpenFull},
			{OpName: TuneSystemdNofile, Params: map[string]string{"limit": "1048576"}},
			{OpName: TuneTimeSyncEnable},
		},
		CertMode:          model.CertModeReality,
		FirewallAllowPort: []int{port},
	}
	if note != "" {
		plan.Notes = append(plan.Notes, note)
	}
	return plan, nil
}

func expandSpeed(probe ProbeResult, in Input) (DeployPlan, error) {
	port, note := pickPort(443, in.PortOverride, probe, "udp")

	password, err := randomAlphanumeric(24)
	if err != nil {
		return DeployPlan{}, err
	}

	certMode, certInput := pickCertMode(in, probe)

	inbound := InboundSpec{
		Engine:   "singbox",
		Protocol: "hysteria2",
		Tag:      "smart-speed-hy2",
		Port:     port,
		Network:  "udp",
		Settings: map[string]any{
			"users": []map[string]any{
				{"name": "default", "password": password},
			},
			"masquerade": "https://www.bing.com",
		},
		Stream: certStreamForHy2TUIC(certMode, certInput, in.Domain),
		Remark: "Smart Deploy — speed (Hysteria2)",
	}

	plan := DeployPlan{
		PresetID: model.PresetSpeed,
		Inbounds: []InboundSpec{inbound},
		Tuning: []TuneSpec{
			{OpName: TuneBBRFQ},
			{OpName: TuneSysctlNetwork},
			{OpName: TuneUDPBuffersLarge, Params: udpBufferParams(probe.NIC.LinkSpeedMbps)},
			{OpName: TuneTCPFastOpenFull},
			{OpName: TuneSystemdNofile, Params: map[string]string{"limit": "1048576"}},
			{OpName: TuneTimeSyncEnable},
		},
		CertMode:          certMode,
		CertInput:         certInput,
		FirewallAllowPort: []int{port},
	}
	if note != "" {
		plan.Notes = append(plan.Notes, note)
	}
	if certMode == model.CertModeSelfSigned {
		plan.Notes = append(plan.Notes,
			"No domain provided — generating a self-signed cert. Clients will need to allow insecure TLS.")
	}
	return plan, nil
}

func expandCombo(probe ProbeResult, in Input) (DeployPlan, error) {
	stable, err := expandStableEgress(probe, in)
	if err != nil {
		return DeployPlan{}, err
	}
	speed, err := expandSpeed(probe, in)
	if err != nil {
		return DeployPlan{}, err
	}

	// Combine: Reality on TCP, Hy2 on UDP. If the speed expander had to
	// fall back because 443/UDP is taken, keep its port. Same for stable.
	plan := DeployPlan{
		PresetID:          model.PresetCombo,
		Inbounds:          append(stable.Inbounds, speed.Inbounds...),
		Tuning:            mergeTuning(stable.Tuning, speed.Tuning),
		CertMode:          speed.CertMode,
		CertInput:         speed.CertInput,
		FirewallAllowPort: mergeInts(stable.FirewallAllowPort, speed.FirewallAllowPort),
		Notes:             append(stable.Notes, speed.Notes...),
	}
	return plan, nil
}

func expandWeakNetwork(probe ProbeResult, in Input) (DeployPlan, error) {
	hy2Port, hy2Note := pickPort(443, in.PortOverride, probe, "udp")
	tuicPort, tuicNote := pickPortExcluding(8443, probe, "udp", []int{hy2Port})

	hy2Password, err := randomAlphanumeric(24)
	if err != nil {
		return DeployPlan{}, err
	}
	tuicUUID := uuid.NewString()
	tuicPassword, err := randomAlphanumeric(24)
	if err != nil {
		return DeployPlan{}, err
	}

	certMode, certInput := pickCertMode(in, probe)
	stream := certStreamForHy2TUIC(certMode, certInput, in.Domain)

	hy2 := InboundSpec{
		Engine:   "singbox",
		Protocol: "hysteria2",
		Tag:      "smart-weaknet-hy2",
		Port:     hy2Port,
		Network:  "udp",
		Settings: map[string]any{
			"users":      []map[string]any{{"name": "default", "password": hy2Password}},
			"masquerade": "https://www.bing.com",
		},
		Stream: stream,
		Remark: "Smart Deploy — weak network (Hysteria2)",
	}

	tuic := InboundSpec{
		Engine:   "singbox",
		Protocol: "tuic",
		Tag:      "smart-weaknet-tuic",
		Port:     tuicPort,
		Network:  "udp",
		Settings: map[string]any{
			"users": []map[string]any{
				{"uuid": tuicUUID, "password": tuicPassword, "name": "default"},
			},
			"congestion_control": "bbr",
		},
		Stream: stream,
		Remark: "Smart Deploy — weak network (TUIC)",
	}

	// Prefer cake when the kernel supports it — it handles high jitter
	// and bufferbloat on mobile links better than plain fq.
	qdiscOp := TuneBBRFQ
	if probe.Kernel.Features.Cake {
		qdiscOp = TuneQdiscCake
	}

	plan := DeployPlan{
		PresetID: model.PresetWeakNetwork,
		Inbounds: []InboundSpec{hy2, tuic},
		Tuning: []TuneSpec{
			{OpName: qdiscOp},
			{OpName: TuneSysctlNetwork},
			{OpName: TuneUDPBuffersLarge, Params: udpBufferParams(probe.NIC.LinkSpeedMbps)},
			{OpName: TuneTCPFastOpenFull},
			{OpName: TuneSystemdNofile, Params: map[string]string{"limit": "1048576"}},
			{OpName: TuneTimeSyncEnable},
		},
		CertMode:          certMode,
		CertInput:         certInput,
		FirewallAllowPort: []int{hy2Port, tuicPort},
	}
	if hy2Note != "" {
		plan.Notes = append(plan.Notes, hy2Note)
	}
	if tuicNote != "" {
		plan.Notes = append(plan.Notes, tuicNote)
	}
	if certMode == model.CertModeSelfSigned {
		plan.Notes = append(plan.Notes,
			"No domain provided — generating a self-signed cert. Clients will need to allow insecure TLS.")
	}
	return plan, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────

// pickPort returns the first fallback that is both free in the probe and
// not in the existing-inbound set. proto is informational (future: probe
// UDP separately); for Phase 1 the TCP probe doubles as a proxy signal.
// A non-empty note is returned when a fallback replaced the preferred port.
func pickPort(preferred, override int, probe ProbeResult, proto string) (int, string) {
	return pickPortExcluding(preferred, probe, proto, nil)
}

func pickPortExcluding(preferred int, probe ProbeResult, proto string, exclude []int) (int, string) {
	if preferred == 0 {
		preferred = 443
	}
	// Honor explicit user override only if it isn't occupied.
	if isPortUsable(preferred, probe, exclude) {
		return preferred, ""
	}
	for _, fb := range portFallbacks {
		if fb == preferred {
			continue
		}
		if isPortUsable(fb, probe, exclude) {
			return fb, fmt.Sprintf("Port %d is in use (%s); falling back to %d.", preferred, proto, fb)
		}
	}
	// All fallbacks taken — return preferred anyway; orchestrator will
	// surface the conflict when it tries to bind.
	return preferred, fmt.Sprintf("No free fallback port found; will attempt %d and may fail.", preferred)
}

func isPortUsable(port int, probe ProbeResult, exclude []int) bool {
	for _, e := range exclude {
		if e == port {
			return false
		}
	}
	for _, used := range probe.InboundPorts {
		if used == port {
			return false
		}
	}
	// If the probe has information about this port and says it's taken,
	// respect that. If the probe didn't check this port, assume usable.
	if free, ok := probe.PortAvail.Ports[port]; ok && !free {
		return false
	}
	return true
}

// pickCertMode returns the appropriate mode based on what the user provided.
// Precedence: existing (user cert paths) > acme (domain + email) > self-signed.
func pickCertMode(in Input, probe ProbeResult) (string, CertInput) {
	if in.Options != nil {
		if cp, _ := in.Options["cert_path"].(string); cp != "" {
			if kp, _ := in.Options["key_path"].(string); kp != "" {
				return model.CertModeExisting, CertInput{CertPath: cp, KeyPath: kp}
			}
		}
	}
	if in.Domain != "" {
		return model.CertModeACME, CertInput{Domain: in.Domain, Email: in.Email}
	}
	return model.CertModeSelfSigned, CertInput{PublicIP: probe.PublicIP.V4}
}

// certStreamForHy2TUIC produces the stream settings used by Hysteria2 and
// TUIC. Both live under sing-box's TLS inbound config shape.
func certStreamForHy2TUIC(mode string, ci CertInput, domain string) map[string]any {
	serverName := domain
	if serverName == "" {
		serverName = ci.PublicIP
	}
	stream := map[string]any{
		"tls": map[string]any{
			"enabled":     true,
			"server_name": serverName,
		},
	}
	tls := stream["tls"].(map[string]any)
	switch mode {
	case model.CertModeACME:
		tls["certificate_path"] = "" // orchestrator fills from CertManager.Provision
		tls["key_path"] = ""
	case model.CertModeSelfSigned:
		tls["certificate_path"] = ""
		tls["key_path"] = ""
		tls["insecure"] = true // surfaces to generated client config
	case model.CertModeExisting:
		tls["certificate_path"] = ci.CertPath
		tls["key_path"] = ci.KeyPath
	}
	return stream
}

// udpBufferParams scales UDP socket buffers to NIC speed. The kernel's
// default rmem_max (~212 KB) is far too small for Hy2/TUIC on a 1 Gbps link.
// We cap at 64 MB to avoid pathological memory consumption on VPSes that
// misreport their link speed.
func udpBufferParams(linkMbps int) map[string]string {
	if linkMbps <= 0 {
		linkMbps = 1000 // assume 1 Gbps when NIC speed is unknown
	}
	// Bandwidth-delay product: linkMbps * 50ms / 8 = KBytes; convert to bytes.
	bdpBytes := int64(linkMbps) * 1024 * 50 / 8
	if bdpBytes < 4*1024*1024 {
		bdpBytes = 4 * 1024 * 1024
	}
	if bdpBytes > 64*1024*1024 {
		bdpBytes = 64 * 1024 * 1024
	}
	return map[string]string{
		"rmem_max": fmt.Sprintf("%d", bdpBytes),
		"wmem_max": fmt.Sprintf("%d", bdpBytes),
	}
}

func mergeTuning(a, b []TuneSpec) []TuneSpec {
	seen := map[string]bool{}
	out := []TuneSpec{}
	for _, t := range a {
		if !seen[t.OpName] {
			seen[t.OpName] = true
			out = append(out, t)
		}
	}
	for _, t := range b {
		if !seen[t.OpName] {
			seen[t.OpName] = true
			out = append(out, t)
		}
	}
	return out
}

func mergeInts(a, b []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, v := range a {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range b {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// generateX25519Keys returns an (private, public) base64-url-encoded pair
// suitable for Xray's Reality protocol.
func generateX25519Keys() (string, string, error) {
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(priv.Bytes()), enc.EncodeToString(priv.PublicKey().Bytes()), nil
}

func randomHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomAlphanumeric returns a URL-safe password of approximately n bytes
// of entropy. We avoid ambiguous characters (0/O, 1/l) by using base64 URL.
func randomAlphanumeric(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}
