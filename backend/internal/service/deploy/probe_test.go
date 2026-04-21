package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────
// Pure parser tests
// ─────────────────────────────────────────────────────────────────────────

func TestParseKernelResultExtractsVersion(t *testing.T) {
	procVer := "Linux version 5.15.0-88-generic (buildd@lcy02) (gcc (Ubuntu 11.4.0-1ubuntu1~22.04))"
	got := parseKernelResult(procVer, "bbr cubic reno", "3", false)

	if got.Version != "5.15.0" {
		t.Errorf("Version = %q, want 5.15.0", got.Version)
	}
	if got.Major != 5 || got.Minor != 15 {
		t.Errorf("Major/Minor = %d/%d, want 5/15", got.Major, got.Minor)
	}
}

func TestParseKernelResultDetectsBBR(t *testing.T) {
	cases := []struct {
		name    string
		availCC string
		wantBBR bool
	}{
		{"bbr listed", "cubic reno bbr", true},
		{"bbr only", "bbr", true},
		{"no bbr", "cubic reno", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseKernelResult("Linux version 5.4.0", tc.availCC, "3", false)
			if got.Features.BBR != tc.wantBBR {
				t.Errorf("BBR = %v, want %v", got.Features.BBR, tc.wantBBR)
			}
		})
	}
}

func TestParseKernelResultInfersFQFromVersion(t *testing.T) {
	// Kernel 4.9+ universally has fq / fq_codel built in.
	got := parseKernelResult("Linux version 4.9.0", "cubic", "0", false)
	if !got.Features.FQ || !got.Features.FQCodel {
		t.Errorf("expected FQ and FQCodel inferred from kernel 4.9, got %+v", got.Features)
	}

	// Very old kernel (3.4) should not claim fq.
	got = parseKernelResult("Linux version 3.4.0", "cubic", "0", false)
	if got.Features.FQ || got.Features.FQCodel {
		t.Errorf("expected no FQ on kernel 3.4, got %+v", got.Features)
	}
}

func TestParseKernelResultTFOFlag(t *testing.T) {
	if !parseKernelResult("Linux version 5.10", "bbr", "3", false).Features.TFO {
		t.Errorf("TFO=3 should set Features.TFO")
	}
	if parseKernelResult("Linux version 5.10", "bbr", "0", false).Features.TFO {
		t.Errorf("TFO=0 should not set Features.TFO")
	}
}

func TestParseOSReleaseDebian(t *testing.T) {
	content := `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
ID=debian
HOME_URL="https://www.debian.org/"
`
	got := parseOSRelease(content)
	if got.ID != "debian" {
		t.Errorf("ID = %q, want debian", got.ID)
	}
	if got.VersionID != "12" {
		t.Errorf("VersionID = %q, want 12", got.VersionID)
	}
	if !strings.Contains(got.PrettyName, "Debian") {
		t.Errorf("PrettyName = %q, want containing Debian", got.PrettyName)
	}
}

func TestParseOSReleaseAlpine(t *testing.T) {
	content := `NAME="Alpine Linux"
ID=alpine
VERSION_ID=3.19.0
`
	got := parseOSRelease(content)
	if got.ID != "alpine" {
		t.Errorf("ID = %q, want alpine", got.ID)
	}
	if got.VersionID != "3.19.0" {
		t.Errorf("VersionID = %q, want 3.19.0", got.VersionID)
	}
}

func TestParseOSReleaseEmpty(t *testing.T) {
	got := parseOSRelease("")
	if got.ID != "unknown" {
		t.Errorf("empty os-release should default ID=unknown, got %q", got.ID)
	}
}

func TestParseTimedatectl(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantActive bool
		wantSynced bool
	}{
		{
			name:       "active and synced",
			input:      "NTP=yes\nNTPSynchronized=yes\n",
			wantActive: true,
			wantSynced: true,
		},
		{
			name:       "active not synced",
			input:      "NTP=yes\nNTPSynchronized=no\n",
			wantActive: true,
			wantSynced: false,
		},
		{
			name:       "inactive",
			input:      "NTP=no\nNTPSynchronized=no\n",
			wantActive: false,
			wantSynced: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTimedatectl(tc.input)
			if got.Service != "systemd-timesyncd" {
				t.Errorf("Service = %q, want systemd-timesyncd", got.Service)
			}
			if got.Active != tc.wantActive {
				t.Errorf("Active = %v, want %v", got.Active, tc.wantActive)
			}
			if got.Synced != tc.wantSynced {
				t.Errorf("Synced = %v, want %v", got.Synced, tc.wantSynced)
			}
		})
	}
}

func TestParseChronycTrackingSynced(t *testing.T) {
	out := `Reference ID    : C6266A33 (time.cloudflare.com)
Stratum         : 3
Leap status     : Normal
`
	got := parseChronycTracking(out)
	if got.Service != "chronyd" {
		t.Errorf("Service = %q, want chronyd", got.Service)
	}
	if !got.Active {
		t.Errorf("Active = false, want true")
	}
	if !got.Synced {
		t.Errorf("Synced = false, want true")
	}
}

func TestParseSystemdVersion(t *testing.T) {
	out := "systemd 252 (252.5-2~deb12u1)\n+PAM +AUDIT -SELINUX"
	if got := parseSystemdVersion(out); got != "252" {
		t.Errorf("got %q, want 252", got)
	}

	if got := parseSystemdVersion(""); got != "" {
		t.Errorf("empty input should return empty, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Detector tests using a fake Runner
// ─────────────────────────────────────────────────────────────────────────

// fakeRunner is a configurable Runner for testing. All fields are optional;
// unset fields return zero values (or simulate "missing" appropriately).
type fakeRunner struct {
	files         map[string][]byte // ReadFile + FileExists results
	execResults   map[string]fakeExecResult
	lookPathSet   map[string]bool
	portFreeSet   map[int]bool
	uid           int
	ipv4          string
	ipv4Err       error
	ipv6          string
	ipv6Err       error
	inboundPorts  []int
	cpuCores      int
	ramBytes      int64
	swapBytes     int64
	nicName       string
	nicSpeedMbps  int
}

type fakeExecResult struct {
	out []byte
	err error
}

func (f *fakeRunner) ReadFile(path string) ([]byte, error) {
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeRunner) FileExists(path string) bool {
	_, ok := f.files[path]
	return ok
}

func (f *fakeRunner) LookPath(name string) bool {
	return f.lookPathSet[name]
}

func (f *fakeRunner) Exec(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.execResults[key]; ok {
		return r.out, r.err
	}
	// Fall back to a match on just the command name so tests can stub broad cases.
	if r, ok := f.execResults[name]; ok {
		return r.out, r.err
	}
	return nil, errors.New("exec not stubbed: " + key)
}

func (f *fakeRunner) PortFreeTCP(port int) bool { return f.portFreeSet[port] }
func (f *fakeRunner) GetUID() int               { return f.uid }
func (f *fakeRunner) FetchPublicIPv4(_ context.Context) (string, error) {
	return f.ipv4, f.ipv4Err
}
func (f *fakeRunner) FetchPublicIPv6(_ context.Context) (string, error) {
	return f.ipv6, f.ipv6Err
}
func (f *fakeRunner) InboundPortsFromDB() ([]int, error) { return f.inboundPorts, nil }
func (f *fakeRunner) CPUCores() (int, error)              { return f.cpuCores, nil }
func (f *fakeRunner) MemInfo() (int64, int64, error)      { return f.ramBytes, f.swapBytes, nil }
func (f *fakeRunner) PrimaryNIC() (string, int, error) {
	if f.nicName == "" {
		return "", 0, errors.New("no nic")
	}
	return f.nicName, f.nicSpeedMbps, nil
}

func TestProbeDetectRoot(t *testing.T) {
	cases := []struct {
		uid    int
		wantOK bool
	}{
		{0, true},
		{1000, false},
		{-1, false},
	}
	for _, tc := range cases {
		p := NewWithRunner(&fakeRunner{uid: tc.uid})
		got := p.detectRoot()
		if got.OK != tc.wantOK {
			t.Errorf("uid=%d: OK=%v, want %v", tc.uid, got.OK, tc.wantOK)
		}
		if got.UID != tc.uid {
			t.Errorf("UID = %d, want %d", got.UID, tc.uid)
		}
	}
}

func TestProbeDetectPortAvail(t *testing.T) {
	r := &fakeRunner{
		portFreeSet: map[int]bool{
			443: false, // taken
			80:  true,
			8443: true,
		},
	}
	p := NewWithRunner(r)
	got := p.detectPortAvail()

	if free, ok := got.Ports[443]; !ok || free {
		t.Errorf("port 443 should be reported as taken (false); got %v (ok=%v)", free, ok)
	}
	if free, ok := got.Ports[80]; !ok || !free {
		t.Errorf("port 80 should be reported as free; got %v (ok=%v)", free, ok)
	}
	// All configured probe ports should be represented.
	if len(got.Ports) != len(portsToProbe) {
		t.Errorf("Ports map has %d entries, want %d", len(got.Ports), len(portsToProbe))
	}
}

func TestProbeDetectFirewallUFWActive(t *testing.T) {
	r := &fakeRunner{
		lookPathSet: map[string]bool{"ufw": true},
		execResults: map[string]fakeExecResult{
			"ufw status": {out: []byte("Status: active\nTo                         Action      From\n")},
		},
	}
	got := NewWithRunner(r).detectFirewall(context.Background())
	if got.Type != "ufw" || !got.Active {
		t.Errorf("got %+v, want Type=ufw Active=true", got)
	}
}

func TestProbeDetectFirewallNone(t *testing.T) {
	got := NewWithRunner(&fakeRunner{}).detectFirewall(context.Background())
	if got.Type != "none" {
		t.Errorf("no firewall binary should give type=none, got %q", got.Type)
	}
}

func TestProbeDetectDockerNotInstalled(t *testing.T) {
	got := NewWithRunner(&fakeRunner{}).detectDocker(context.Background())
	if got.Present || got.Running {
		t.Errorf("no docker binary should give Present=false Running=false, got %+v", got)
	}
}

func TestProbeDetectDockerRunning(t *testing.T) {
	r := &fakeRunner{
		lookPathSet: map[string]bool{"docker": true},
		execResults: map[string]fakeExecResult{
			"docker version --format {{.Server.Version}}": {out: []byte("24.0.7\n")},
		},
	}
	got := NewWithRunner(r).detectDocker(context.Background())
	if !got.Present || !got.Running || got.Version != "24.0.7" {
		t.Errorf("got %+v, want Present=true Running=true Version=24.0.7", got)
	}
}

func TestProbeRunCompletesWithinBudget(t *testing.T) {
	r := &fakeRunner{
		files: map[string][]byte{
			"/proc/version":                                  []byte("Linux version 5.15.0"),
			"/proc/sys/net/ipv4/tcp_available_congestion_control": []byte("bbr cubic"),
			"/proc/sys/net/ipv4/tcp_fastopen":                []byte("3"),
			"/etc/os-release":                                []byte("ID=debian\nVERSION_ID=\"12\"\n"),
			"/run/systemd/system":                            []byte(""),
		},
		lookPathSet: map[string]bool{"timedatectl": true, "docker": true, "ufw": true},
		execResults: map[string]fakeExecResult{
			"systemctl --version":                                 {out: []byte("systemd 252\n")},
			"timedatectl show --property=NTP,NTPSynchronized":     {out: []byte("NTP=yes\nNTPSynchronized=yes\n")},
			"ufw status":                                          {out: []byte("Status: active\n")},
			"docker version --format {{.Server.Version}}":         {out: []byte("24.0.7\n")},
		},
		uid:          0,
		ipv4:         "1.2.3.4",
		cpuCores:     2,
		ramBytes:     2 * 1024 * 1024 * 1024,
		nicName:      "eth0",
		nicSpeedMbps: 1000,
		portFreeSet:  map[int]bool{443: true, 80: true, 8443: true},
		inboundPorts: []int{20000},
	}

	start := time.Now()
	got := NewWithRunner(r).Run(context.Background())
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("probe exceeded 5s budget: %s", elapsed)
	}
	if !got.RootCheck.OK {
		t.Errorf("root check: OK=false, want true")
	}
	if got.Kernel.Major != 5 {
		t.Errorf("kernel Major=%d, want 5", got.Kernel.Major)
	}
	if got.Distro.ID != "debian" {
		t.Errorf("distro ID=%q, want debian", got.Distro.ID)
	}
	if !got.TimeSync.Synced {
		t.Errorf("time sync: Synced=false, want true")
	}
	if got.PublicIP.V4 != "1.2.3.4" {
		t.Errorf("public IP V4=%q, want 1.2.3.4", got.PublicIP.V4)
	}
	if got.Hardware.CPUCores != 2 {
		t.Errorf("CPU cores=%d, want 2", got.Hardware.CPUCores)
	}
	if got.NIC.Primary != "eth0" {
		t.Errorf("primary NIC=%q, want eth0", got.NIC.Primary)
	}
	if got.Firewall.Type != "ufw" || !got.Firewall.Active {
		t.Errorf("firewall=%+v, want ufw active", got.Firewall)
	}
	if !got.Docker.Present || !got.Docker.Running {
		t.Errorf("docker=%+v, want Present=true Running=true", got.Docker)
	}
	if len(got.InboundPorts) != 1 || got.InboundPorts[0] != 20000 {
		t.Errorf("inbound ports=%v, want [20000]", got.InboundPorts)
	}
	if got.ProbedAt.IsZero() {
		t.Errorf("ProbedAt was not populated")
	}
	if got.DurationMs < 0 {
		t.Errorf("DurationMs=%d, want >= 0", got.DurationMs)
	}
}
