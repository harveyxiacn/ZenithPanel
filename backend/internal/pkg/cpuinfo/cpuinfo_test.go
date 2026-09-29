package cpuinfo

import "testing"

const intelXeon = `processor	: 0
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Platinum 8375C CPU @ 2.90GHz
flags		: fpu vme sse4_2 pclmulqdq aes avx avx2 avx512f hypervisor
processor	: 1
vendor_id	: GenuineIntel
model name	: Intel(R) Xeon(R) Platinum 8375C CPU @ 2.90GHz
flags		: fpu vme sse4_2 pclmulqdq aes avx avx2 avx512f hypervisor
`

const amdEpyc = `processor	: 0
vendor_id	: AuthenticAMD
model name	: AMD EPYC 7763 64-Core Processor
flags		: fpu sse4_2 pclmulqdq aes avx avx2 hypervisor
`

const qemuVirtual = `processor	: 0
vendor_id	: GenuineIntel
model name	: QEMU Virtual CPU version 2.5+
flags		: fpu sse sse2 hypervisor
`

const ampereAltra = `processor	: 0
BogoMIPS	: 50.00
Features	: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid asimdrdm lrcpc dcpop asimddp
CPU implementer	: 0x41
CPU architecture: 8
CPU variant	: 0x3
CPU part	: 0xd0c
processor	: 1
Features	: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid asimdrdm lrcpc dcpop asimddp
CPU implementer	: 0x41
CPU part	: 0xd0c
processor	: 2
processor	: 3
`

const gravitonV1 = `processor	: 0
Features	: fp asimd aes pmull sha1 sha2 crc32 atomics sve
CPU implementer	: 0x41
CPU part	: 0xd40
`

const ampereOne = `processor	: 0
Features	: fp asimd aes pmull sha1 sha2 atomics
CPU implementer	: 0xc0
CPU part	: 0xac3
`

// Raspberry Pi 4's Cortex-A72 ships without the ARMv8 Crypto Extension.
const raspberryPi4 = `processor	: 0
Features	: fp asimd evtstrm crc32 cpuid
CPU implementer	: 0x41
CPU part	: 0xd08
`

func TestParse(t *testing.T) {
	cases := []struct {
		name, raw, arch string
		vendor, model   string
		cores           int
		fastAES, hidden bool
		advice          string
	}{
		{"intel xeon", intelXeon, "amd64", VendorIntel, "Intel(R) Xeon(R) Platinum 8375C CPU @ 2.90GHz", 2, true, false, "aes_hw"},
		{"amd epyc", amdEpyc, "amd64", VendorAMD, "AMD EPYC 7763 64-Core Processor", 1, true, false, "aes_hw"},
		{"qemu64 hides aes", qemuVirtual, "amd64", VendorIntel, "QEMU Virtual CPU version 2.5+", 1, false, true, "no_aes_hw"},
		{"ampere altra (neoverse-n1)", ampereAltra, "arm64", VendorARM, "Neoverse-N1", 4, true, false, "aes_hw"},
		{"graviton3 (neoverse-v1)", gravitonV1, "arm64", VendorARM, "Neoverse-V1", 1, true, false, "aes_hw"},
		{"ampereone", ampereOne, "arm64", VendorAmpere, "AmpereOne", 1, true, false, "aes_hw"},
		{"raspberry pi 4", raspberryPi4, "arm64", VendorARM, "Cortex-A72", 1, false, false, "no_aes_hw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.raw, tc.arch)
			if got.Vendor != tc.vendor || got.Model != tc.model || got.Cores != tc.cores {
				t.Fatalf("vendor/model/cores = %q/%q/%d, want %q/%q/%d", got.Vendor, got.Model, got.Cores, tc.vendor, tc.model, tc.cores)
			}
			if got.FastAES() != tc.fastAES || got.AESHidden != tc.hidden {
				t.Fatalf("fastAES/hidden = %v/%v, want %v/%v", got.FastAES(), got.AESHidden, tc.fastAES, tc.hidden)
			}
			adv := got.Advice()
			if len(adv) == 0 || adv[0].Code != tc.advice {
				t.Fatalf("Advice()[0] = %+v, want code %q", adv, tc.advice)
			}
			if tc.hidden && (len(adv) < 2 || adv[1].Code != "aes_hidden") {
				t.Fatalf("expected aes_hidden advice, got %+v", adv)
			}
			wantCipher := CipherChaCha
			if tc.fastAES {
				wantCipher = CipherAES
			}
			if got.PreferredCipher != wantCipher {
				t.Fatalf("PreferredCipher = %q, want %q", got.PreferredCipher, wantCipher)
			}
		})
	}
}

func TestParseArchSpecificFlags(t *testing.T) {
	x := Parse(intelXeon, "amd64")
	if !x.AVX2 || !x.AVX512 || !x.Hypervisor || x.SVE {
		t.Fatalf("intel flags wrong: %+v", x)
	}
	g := Parse(gravitonV1, "arm64")
	if !g.SVE || !g.Atomics || g.AVX2 {
		t.Fatalf("graviton flags wrong: %+v", g)
	}
}
