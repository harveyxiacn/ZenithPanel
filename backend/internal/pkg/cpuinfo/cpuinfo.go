// Package cpuinfo detects the host CPU family and the features that matter
// for proxy throughput — chiefly whether AES is hardware-accelerated, which
// decides between AES-GCM and ChaCha20-Poly1305 (a 5–30× difference in
// single-core AEAD throughput either way).
package cpuinfo

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// Vendor families the panel tunes for.
const (
	VendorIntel   = "intel"
	VendorAMD     = "amd"
	VendorARM     = "arm"    // Arm Ltd. cores (Neoverse/Cortex: Graviton, Ampere Altra, Cobalt, Axion…)
	VendorAmpere  = "ampere" // Ampere custom cores (AmpereOne)
	VendorOther   = "other"
	VendorUnknown = "unknown"
)

// Preferred AEAD families.
const (
	CipherAES    = "aes-gcm"
	CipherChaCha = "chacha20-poly1305"
)

// Info describes the host CPU.
type Info struct {
	Arch       string `json:"arch"`       // GOARCH: amd64, arm64, …
	Vendor     string `json:"vendor"`     // one of the Vendor* constants
	Model      string `json:"model"`      // human-readable model / core name
	Cores      int    `json:"cores"`      // logical CPUs visible to the process
	AES        bool   `json:"aes"`        // hardware AES (AES-NI / ARMv8 Crypto)
	CLMUL      bool   `json:"clmul"`      // carry-less multiply (PCLMULQDQ / PMULL) — needed for fast GHASH
	AVX2       bool   `json:"avx2"`       // x86 only
	AVX512     bool   `json:"avx512"`     // x86 only
	SVE        bool   `json:"sve"`        // arm64 only
	Atomics    bool   `json:"atomics"`    // arm64 LSE atomics
	Hypervisor bool   `json:"hypervisor"` // running under a hypervisor
	// AESHidden is set when the CPU model is known to support AES but the
	// hypervisor masks the flag (generic `qemu64`/`kvm64` CPU models on cheap
	// KVM VPSes). Worth surfacing: switching the VM CPU type to `host`
	// recovers a large amount of crypto throughput.
	AESHidden bool `json:"aes_hidden"`
	// PreferredCipher is CipherAES when AES+CLMUL are in hardware, otherwise
	// CipherChaCha.
	PreferredCipher string `json:"preferred_cipher"`
}

// FastAES reports whether AES-GCM runs in hardware on this CPU.
func (i Info) FastAES() bool { return i.AES && i.CLMUL }

// Advice is a machine-readable recommendation derived from the CPU; the
// frontend localises it by Code.
type Advice struct {
	Code  string `json:"code"`
	Level string `json:"level"` // info | warn
	Text  string `json:"text"`  // English fallback
}

// Advice returns proxy-relevant recommendations for this CPU.
//
// TLS 1.3 / QUIC protocols (VLESS+Reality, Trojan, Hysteria2, TUIC) need no
// configuration: Go's TLS server already prefers ChaCha20-Poly1305 when the
// CPU lacks AES hardware. Shadowsocks-2022 is the exception — its
// multi-user mode (what the panel uses) only supports the AES methods in
// both Xray and sing-box, so on CPUs without AES it is the slowest choice.
func (i Info) Advice() []Advice {
	if i.Vendor == VendorUnknown && i.Model == "" {
		return nil // /proc/cpuinfo unavailable — don't guess
	}
	var out []Advice
	if i.FastAES() {
		out = append(out, Advice{Code: "aes_hw", Level: "info",
			Text: "Hardware AES available: AES-GCM ciphers run at full speed; prefer 2022-blake3-aes-128-gcm for Shadowsocks."})
	} else {
		out = append(out, Advice{Code: "no_aes_hw", Level: "warn",
			Text: "No hardware AES: prefer VLESS+Reality, Hysteria2 or TUIC (they negotiate ChaCha20 automatically); avoid Shadowsocks-2022, whose multi-user mode is AES-only."})
	}
	if i.AESHidden {
		out = append(out, Advice{Code: "aes_hidden", Level: "warn",
			Text: "The VM's CPU model hides AES-NI. Ask your provider (or set the CPU type to \"host\") to expose it — crypto throughput can improve 5-25x."})
	}
	return out
}

var (
	once   sync.Once
	cached Info
)

// Detect returns the host CPU info, read once and cached.
func Detect() Info {
	once.Do(func() {
		raw, _ := os.ReadFile("/proc/cpuinfo")
		cached = Parse(string(raw), runtime.GOARCH)
		if cached.Cores == 0 {
			cached.Cores = runtime.NumCPU()
		}
	})
	return cached
}

// Parse builds Info from /proc/cpuinfo content for the given GOARCH.
func Parse(cpuinfo, arch string) Info {
	info := Info{Arch: arch, Vendor: VendorUnknown}
	first := map[string]string{} // first value seen for each key
	processors := 0
	for _, line := range strings.Split(cpuinfo, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "processor" {
			processors++
			continue
		}
		if _, seen := first[k]; !seen {
			first[k] = v
		}
	}
	info.Cores = processors

	switch arch {
	case "amd64", "386":
		parseX86(&info, first)
	case "arm64", "arm":
		parseARM(&info, first)
	}

	info.PreferredCipher = CipherChaCha
	if info.FastAES() {
		info.PreferredCipher = CipherAES
	}
	return info
}

func flagSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Fields(s) {
		set[f] = true
	}
	return set
}

func parseX86(info *Info, kv map[string]string) {
	switch kv["vendor_id"] {
	case "GenuineIntel":
		info.Vendor = VendorIntel
	case "AuthenticAMD", "HygonGenuine":
		info.Vendor = VendorAMD
	case "":
		info.Vendor = VendorUnknown
	default:
		info.Vendor = VendorOther
	}
	info.Model = kv["model name"]
	flags := flagSet(kv["flags"])
	info.AES = flags["aes"]
	info.CLMUL = flags["pclmulqdq"]
	info.AVX2 = flags["avx2"]
	info.AVX512 = flags["avx512f"]
	info.Hypervisor = flags["hypervisor"]

	// Generic QEMU CPU models report themselves by name and mask AES-NI even
	// when the physical host has it (every x86 server CPU since ~2010 does).
	m := strings.ToLower(info.Model)
	if info.Hypervisor && !info.AES &&
		(strings.Contains(m, "qemu") || strings.Contains(m, "kvm") || strings.Contains(m, "common kvm")) {
		info.AESHidden = true
	}
}

// Arm "CPU implementer" and "CPU part" codes for server-class and common cores.
var armParts = map[string]map[string]string{
	"0x41": { // Arm Ltd.
		"0xd03": "Cortex-A53",
		"0xd07": "Cortex-A57",
		"0xd08": "Cortex-A72",
		"0xd0b": "Cortex-A76",
		"0xd0c": "Neoverse-N1",
		"0xd40": "Neoverse-V1",
		"0xd49": "Neoverse-N2",
		"0xd4f": "Neoverse-V2",
		"0xd8e": "Neoverse-N3",
		"0xd84": "Neoverse-V3",
	},
	"0xc0": { // Ampere Computing
		"0xac3": "AmpereOne",
		"0xac4": "AmpereOne",
		"0xac5": "AmpereOne",
	},
	"0x48": { // HiSilicon
		"0xd01": "Kunpeng 920",
	},
}

func parseARM(info *Info, kv map[string]string) {
	impl := strings.ToLower(kv["CPU implementer"])
	part := strings.ToLower(kv["CPU part"])
	switch impl {
	case "0x41":
		info.Vendor = VendorARM
	case "0xc0":
		info.Vendor = VendorAmpere
	case "":
		info.Vendor = VendorUnknown
	default:
		info.Vendor = VendorOther
	}
	if name, ok := armParts[impl][part]; ok {
		info.Model = name
	} else if m := kv["model name"]; m != "" {
		info.Model = m
	} else if impl != "" {
		info.Model = "implementer " + impl + " part " + part
	}
	feats := flagSet(kv["Features"])
	info.AES = feats["aes"]
	info.CLMUL = feats["pmull"]
	info.SVE = feats["sve"]
	info.Atomics = feats["atomics"]
	// arm64 guests don't expose a hypervisor flag in cpuinfo; leave false.
}
