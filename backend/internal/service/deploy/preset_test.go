package deploy

import (
	"strings"
	"testing"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

// baseProbe returns a realistic probe with 443 free, kernel 5.15, 1Gbps NIC.
func baseProbe() ProbeResult {
	return ProbeResult{
		RootCheck: RootCheckResult{OK: true, UID: 0},
		Kernel: KernelResult{
			Version:  "5.15.0",
			Major:    5,
			Minor:    15,
			Features: KernelFeatures{BBR: true, FQ: true, FQCodel: true, TFO: true},
		},
		Distro:   DistroResult{ID: "debian", VersionID: "12"},
		PublicIP: PublicIPResult{V4: "1.2.3.4"},
		NIC:      NICResult{Primary: "eth0", LinkSpeedMbps: 1000},
		PortAvail: PortAvailResult{
			Ports: map[int]bool{443: true, 80: true, 8443: true, 2053: true},
		},
		InboundPorts: []int{},
	}
}

func TestExpandUnknownPresetReturnsError(t *testing.T) {
	_, err := Expand("not_a_preset", baseProbe(), Input{})
	if err == nil {
		t.Fatalf("expected error for unknown preset")
	}
}

func TestExpandStableEgressDefaults(t *testing.T) {
	plan, err := Expand(model.PresetStableEgress, baseProbe(), Input{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.PresetID != model.PresetStableEgress {
		t.Errorf("PresetID = %q", plan.PresetID)
	}
	if len(plan.Inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(plan.Inbounds))
	}
	ib := plan.Inbounds[0]
	if ib.Engine != "xray" || ib.Protocol != "vless" {
		t.Errorf("inbound = %s/%s, want xray/vless", ib.Engine, ib.Protocol)
	}
	if ib.Port != 443 {
		t.Errorf("Port = %d, want 443", ib.Port)
	}
	if ib.Network != "tcp" {
		t.Errorf("Network = %q, want tcp", ib.Network)
	}
	if plan.CertMode != model.CertModeReality {
		t.Errorf("CertMode = %q, want reality", plan.CertMode)
	}

	// Reality stream settings contain the target SNI + keypair.
	stream, ok := ib.Stream["realitySettings"].(map[string]any)
	if !ok {
		t.Fatalf("realitySettings missing from stream")
	}
	if stream["dest"] != defaultRealityTarget+":443" {
		t.Errorf("reality dest = %v, want %s:443", stream["dest"], defaultRealityTarget)
	}
	if stream["privateKey"] == "" || stream["publicKey"] == "" {
		t.Errorf("reality keys were not generated: %v", stream)
	}
}

func TestExpandStableEgressFallsBackWhenPort443Taken(t *testing.T) {
	p := baseProbe()
	p.PortAvail.Ports[443] = false // taken

	plan, err := Expand(model.PresetStableEgress, p, Input{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.Inbounds[0].Port == 443 {
		t.Errorf("expected fallback from 443, got 443 still")
	}
	if plan.Inbounds[0].Port != 8443 {
		t.Errorf("expected fallback to 8443, got %d", plan.Inbounds[0].Port)
	}
	foundNote := false
	for _, n := range plan.Notes {
		if strings.Contains(n, "443") && strings.Contains(n, "8443") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Errorf("expected a fallback note mentioning 443→8443, got %v", plan.Notes)
	}
}

func TestExpandStableEgressRespectsRealityTargetOverride(t *testing.T) {
	plan, err := Expand(model.PresetStableEgress, baseProbe(), Input{RealityTarget: "www.apple.com"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	reality := plan.Inbounds[0].Stream["realitySettings"].(map[string]any)
	if reality["dest"] != "www.apple.com:443" {
		t.Errorf("reality dest = %v, want www.apple.com:443", reality["dest"])
	}
}

func TestExpandSpeedWithDomainUsesACME(t *testing.T) {
	plan, err := Expand(model.PresetSpeed, baseProbe(), Input{Domain: "proxy.example.com", Email: "me@example.com"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.CertMode != model.CertModeACME {
		t.Errorf("CertMode = %q, want acme", plan.CertMode)
	}
	if plan.CertInput.Domain != "proxy.example.com" {
		t.Errorf("CertInput.Domain = %q, want proxy.example.com", plan.CertInput.Domain)
	}
	if plan.Inbounds[0].Protocol != "hysteria2" {
		t.Errorf("Protocol = %q, want hysteria2", plan.Inbounds[0].Protocol)
	}
}

func TestExpandSpeedWithoutDomainUsesSelfSigned(t *testing.T) {
	plan, err := Expand(model.PresetSpeed, baseProbe(), Input{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.CertMode != model.CertModeSelfSigned {
		t.Errorf("CertMode = %q, want self_signed", plan.CertMode)
	}
	if plan.CertInput.PublicIP != "1.2.3.4" {
		t.Errorf("CertInput.PublicIP = %q, want 1.2.3.4", plan.CertInput.PublicIP)
	}
	// Note about insecure TLS should surface to the user.
	foundNote := false
	for _, n := range plan.Notes {
		if strings.Contains(strings.ToLower(n), "self-signed") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Errorf("expected self-signed note, got %v", plan.Notes)
	}
}

func TestExpandSpeedWithExistingCertPaths(t *testing.T) {
	in := Input{
		Options: map[string]any{
			"cert_path": "/etc/my/fullchain.pem",
			"key_path":  "/etc/my/privkey.pem",
		},
	}
	plan, err := Expand(model.PresetSpeed, baseProbe(), in)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.CertMode != model.CertModeExisting {
		t.Errorf("CertMode = %q, want existing", plan.CertMode)
	}
	if plan.CertInput.CertPath != "/etc/my/fullchain.pem" || plan.CertInput.KeyPath != "/etc/my/privkey.pem" {
		t.Errorf("CertInput = %+v, want the provided paths", plan.CertInput)
	}
}

func TestExpandComboHasTwoInboundsAndMergedTuning(t *testing.T) {
	plan, err := Expand(model.PresetCombo, baseProbe(), Input{Domain: "proxy.example.com"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(plan.Inbounds) != 2 {
		t.Fatalf("expected 2 inbounds, got %d", len(plan.Inbounds))
	}
	protos := []string{plan.Inbounds[0].Protocol, plan.Inbounds[1].Protocol}
	if !contains(protos, "vless") || !contains(protos, "hysteria2") {
		t.Errorf("Inbound protocols = %v, want vless + hysteria2", protos)
	}
	// Tuning should be deduplicated: BBR+FQ should appear once even though
	// both sub-presets request it.
	bbrCount := 0
	for _, t := range plan.Tuning {
		if t.OpName == TuneBBRFQ {
			bbrCount++
		}
	}
	if bbrCount != 1 {
		t.Errorf("BBR+FQ op count = %d, want 1 after merge", bbrCount)
	}
}

func TestExpandWeakNetworkUsesCakeWhenAvailable(t *testing.T) {
	p := baseProbe()
	p.Kernel.Features.Cake = true

	plan, err := Expand(model.PresetWeakNetwork, p, Input{Domain: "x.example.com"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	foundCake := false
	for _, t := range plan.Tuning {
		if t.OpName == TuneQdiscCake {
			foundCake = true
		}
	}
	if !foundCake {
		t.Errorf("expected cake qdisc when kernel supports it, tuning = %v", plan.Tuning)
	}
}

func TestExpandWeakNetworkTUICPortDifferentFromHy2(t *testing.T) {
	plan, err := Expand(model.PresetWeakNetwork, baseProbe(), Input{Domain: "x.example.com"})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(plan.Inbounds) != 2 {
		t.Fatalf("expected 2 inbounds, got %d", len(plan.Inbounds))
	}
	if plan.Inbounds[0].Port == plan.Inbounds[1].Port {
		t.Errorf("Hy2 and TUIC must bind different ports, got both %d", plan.Inbounds[0].Port)
	}
}

func TestExpandStableEgressSkipsExistingInboundPorts(t *testing.T) {
	p := baseProbe()
	p.InboundPorts = []int{443} // existing manual inbound on 443

	plan, err := Expand(model.PresetStableEgress, p, Input{})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if plan.Inbounds[0].Port == 443 {
		t.Errorf("port 443 already used by another inbound, should fall back")
	}
}

func TestUDPBufferParamsScalesWithNIC(t *testing.T) {
	slow := udpBufferParams(100)   // 100 Mbps
	fast := udpBufferParams(10000) // 10 Gbps

	if slow["rmem_max"] == fast["rmem_max"] {
		t.Errorf("expected different buffer sizes for different NIC speeds")
	}
}

func TestUDPBufferParamsCapsAt64MiB(t *testing.T) {
	got := udpBufferParams(1_000_000) // absurdly fast
	if got["rmem_max"] != "67108864" {
		t.Errorf("rmem_max = %q, want capped at 67108864 (64 MiB)", got["rmem_max"])
	}
}

func TestGenerateX25519KeysAreBase64URLAndDistinct(t *testing.T) {
	priv1, pub1, err := generateX25519Keys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	priv2, _, err := generateX25519Keys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if priv1 == priv2 {
		t.Errorf("two calls produced the same private key")
	}
	if priv1 == "" || pub1 == "" {
		t.Errorf("empty key returned")
	}
	// Base64 URL with no padding: no '=' or '+' or '/'.
	if strings.ContainsAny(priv1, "=+/") {
		t.Errorf("private key has non-urlsafe chars: %q", priv1)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
