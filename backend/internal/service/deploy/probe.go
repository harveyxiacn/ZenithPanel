package deploy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"gorm.io/gorm"
)

// Runner abstracts the OS-facing operations the probe needs so that tests can
// feed deterministic data instead of mocking /proc and shelling out. The
// default runner (newDefaultRunner) wires these to real syscalls and binaries.
type Runner interface {
	ReadFile(path string) ([]byte, error)
	FileExists(path string) bool
	LookPath(name string) bool
	Exec(ctx context.Context, name string, args ...string) ([]byte, error)
	PortFreeTCP(port int) bool
	GetUID() int
	FetchPublicIPv4(ctx context.Context) (string, error)
	FetchPublicIPv6(ctx context.Context) (string, error)
	InboundPortsFromDB() ([]int, error)
	CPUCores() (int, error)
	MemInfo() (ramBytes, swapBytes int64, err error)
	PrimaryNIC() (name string, linkMbps int, err error)
}

// Probe runs the environment detection pipeline.
type Probe struct {
	runner Runner
}

// New returns a Probe wired to the production runner. db may be nil if the
// InboundPorts detector is not needed (useful in tests).
func New(db *gorm.DB) *Probe {
	return &Probe{runner: newDefaultRunner(db)}
}

// NewWithRunner returns a Probe using a caller-supplied runner. Tests use this.
func NewWithRunner(r Runner) *Probe {
	return &Probe{runner: r}
}

// Run executes all detectors within an overall 5-second budget. Individual
// detectors may use the context to cap their own external calls. The returned
// ProbeResult is always populated with whatever each detector managed to
// gather; partial failures are recorded in the per-detector Error fields
// rather than aborting the probe.
func (p *Probe) Run(parent context.Context) ProbeResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()

	result := ProbeResult{ProbedAt: start}

	result.RootCheck = p.detectRoot()
	result.Kernel = p.detectKernel()
	result.Systemd = p.detectSystemd(ctx)
	result.Distro = p.detectDistro()
	result.TimeSync = p.detectTimeSync(ctx)
	result.PublicIP = p.detectPublicIP(ctx)
	result.Hardware = p.detectHardware()
	result.NIC = p.detectNIC()
	result.PortAvail = p.detectPortAvail()
	result.InboundPorts = p.detectInboundPorts()
	result.Firewall = p.detectFirewall(ctx)
	result.Docker = p.detectDocker(ctx)

	result.DurationMs = time.Since(start).Milliseconds()
	return result
}

// ─────────────────────────────────────────────────────────────────────────
// Detectors — thin wrappers over Runner + pure parsers
// ─────────────────────────────────────────────────────────────────────────

func (p *Probe) detectRoot() RootCheckResult {
	uid := p.runner.GetUID()
	return RootCheckResult{
		OK:  uid == 0,
		UID: uid,
	}
}

func (p *Probe) detectKernel() KernelResult {
	procVer, _ := p.runner.ReadFile("/proc/version")
	availCC, _ := p.runner.ReadFile("/proc/sys/net/ipv4/tcp_available_congestion_control")
	tfo, _ := p.runner.ReadFile("/proc/sys/net/ipv4/tcp_fastopen")
	hasCake := p.runner.FileExists("/sys/module/sch_cake") ||
		p.runner.FileExists("/lib/modules") && checkCakeInModules(p.runner)
	return parseKernelResult(string(procVer), string(availCC), string(tfo), hasCake)
}

func (p *Probe) detectSystemd(ctx context.Context) SystemdResult {
	// systemd runs as PID 1 when the init system is systemd.
	if !p.runner.FileExists("/run/systemd/system") {
		return SystemdResult{Present: false}
	}
	out, err := p.runner.Exec(ctx, "systemctl", "--version")
	if err != nil {
		return SystemdResult{Present: true}
	}
	return SystemdResult{Present: true, Version: parseSystemdVersion(string(out))}
}

func (p *Probe) detectDistro() DistroResult {
	data, err := p.runner.ReadFile("/etc/os-release")
	if err != nil {
		return DistroResult{ID: "unknown"}
	}
	return parseOSRelease(string(data))
}

func (p *Probe) detectTimeSync(ctx context.Context) TimeSyncResult {
	// Prefer timedatectl (systemd-timesyncd) when available; fall back to
	// chronyc when chronyd is installed; otherwise report none.
	if p.runner.LookPath("timedatectl") {
		out, err := p.runner.Exec(ctx, "timedatectl", "show", "--property=NTP,NTPSynchronized")
		if err == nil {
			return parseTimedatectl(string(out))
		}
	}
	if p.runner.LookPath("chronyc") {
		out, err := p.runner.Exec(ctx, "chronyc", "tracking")
		if err == nil {
			return parseChronycTracking(string(out))
		}
	}
	return TimeSyncResult{Service: "none"}
}

func (p *Probe) detectPublicIP(ctx context.Context) PublicIPResult {
	res := PublicIPResult{}
	if v4, err := p.runner.FetchPublicIPv4(ctx); err == nil {
		res.V4 = v4
	} else {
		res.Error = err.Error()
	}
	if v6, err := p.runner.FetchPublicIPv6(ctx); err == nil {
		res.V6 = v6
	}
	return res
}

func (p *Probe) detectHardware() HardwareResult {
	cores, _ := p.runner.CPUCores()
	ram, swap, _ := p.runner.MemInfo()
	return HardwareResult{
		CPUCores:  cores,
		RAMBytes:  ram,
		SwapBytes: swap,
	}
}

func (p *Probe) detectNIC() NICResult {
	iface, mbps, err := p.runner.PrimaryNIC()
	if err != nil {
		return NICResult{}
	}
	return NICResult{Primary: iface, LinkSpeedMbps: mbps}
}

// portsToProbe is the fixed list of common proxy ports plus a sparse sample
// of a high-range band used for fallbacks. Keep this small: each port costs
// a tiny TCP bind attempt.
var portsToProbe = []int{80, 443, 8443, 2053, 2083, 2087, 2096, 10443, 20443}

func (p *Probe) detectPortAvail() PortAvailResult {
	out := make(map[int]bool, len(portsToProbe))
	for _, port := range portsToProbe {
		out[port] = p.runner.PortFreeTCP(port)
	}
	return PortAvailResult{Ports: out}
}

func (p *Probe) detectInboundPorts() []int {
	ports, err := p.runner.InboundPortsFromDB()
	if err != nil {
		return []int{}
	}
	return ports
}

func (p *Probe) detectFirewall(ctx context.Context) FirewallResult {
	// Order matches preference: ufw (Ubuntu default), firewalld (RHEL default),
	// nftables (modern Debian), iptables (legacy).
	if p.runner.LookPath("ufw") {
		out, err := p.runner.Exec(ctx, "ufw", "status")
		if err == nil {
			return FirewallResult{Type: "ufw", Active: strings.Contains(string(out), "Status: active")}
		}
		return FirewallResult{Type: "ufw"}
	}
	if p.runner.LookPath("firewall-cmd") {
		out, err := p.runner.Exec(ctx, "firewall-cmd", "--state")
		if err == nil {
			return FirewallResult{Type: "firewalld", Active: strings.TrimSpace(string(out)) == "running"}
		}
		return FirewallResult{Type: "firewalld"}
	}
	if p.runner.LookPath("nft") {
		out, err := p.runner.Exec(ctx, "nft", "list", "ruleset")
		active := err == nil && len(bytes.TrimSpace(out)) > 0
		return FirewallResult{Type: "nftables", Active: active}
	}
	if p.runner.LookPath("iptables") {
		out, err := p.runner.Exec(ctx, "iptables", "-S")
		active := err == nil && bytes.Count(out, []byte("\n")) > 3 // more than just default policies
		return FirewallResult{Type: "iptables", Active: active}
	}
	return FirewallResult{Type: "none"}
}

func (p *Probe) detectDocker(ctx context.Context) DockerResult {
	if !p.runner.LookPath("docker") {
		return DockerResult{}
	}
	out, err := p.runner.Exec(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return DockerResult{Present: true, Running: false}
	}
	return DockerResult{Present: true, Running: true, Version: strings.TrimSpace(string(out))}
}

// ─────────────────────────────────────────────────────────────────────────
// Pure parsers — trivially unit-testable
// ─────────────────────────────────────────────────────────────────────────

var kernelVersionRE = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// parseKernelResult extracts kernel version + feature flags from /proc/version
// style input and /proc/sys/net/ipv4/tcp_available_congestion_control contents.
// A zero procCC input produces Features{} (no flags); callers should treat an
// empty Version as "unknown kernel" rather than a failure.
func parseKernelResult(procVersion, availCC, tfo string, hasCake bool) KernelResult {
	r := KernelResult{}

	if m := kernelVersionRE.FindStringSubmatch(procVersion); m != nil {
		r.Version = m[0]
		r.Major, _ = strconv.Atoi(m[1])
		r.Minor, _ = strconv.Atoi(m[2])
	}

	ccs := strings.Fields(strings.TrimSpace(availCC))
	for _, cc := range ccs {
		if cc == "bbr" {
			r.Features.BBR = true
		}
	}

	r.Features.Cake = hasCake

	// fq and fq_codel are built into every modern kernel (>= 3.6 and >= 3.5
	// respectively). We trust the kernel version as the signal rather than
	// probing module lists.
	if r.Major > 3 || (r.Major == 3 && r.Minor >= 6) {
		r.Features.FQ = true
		r.Features.FQCodel = true
	}

	if strings.TrimSpace(tfo) != "" && strings.TrimSpace(tfo) != "0" {
		r.Features.TFO = true
	}

	return r
}

// parseSystemdVersion pulls the numeric version out of `systemctl --version`
// which looks like "systemd 252 (252.5-2~deb12u1)\n+PAM +AUDIT ...".
func parseSystemdVersion(out string) string {
	scanner := bufio.NewScanner(strings.NewReader(out))
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.EqualFold(fields[0], "systemd") {
			return fields[1]
		}
	}
	return ""
}

// parseOSRelease parses /etc/os-release key=value content. Values may be
// quoted; this strips surrounding double-quotes.
func parseOSRelease(content string) DistroResult {
	r := DistroResult{ID: "unknown"}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		switch key {
		case "ID":
			r.ID = strings.ToLower(val)
		case "VERSION_ID":
			r.VersionID = val
		case "PRETTY_NAME":
			r.PrettyName = val
		}
	}
	return r
}

// parseTimedatectl reads key=value output of `timedatectl show
// --property=NTP,NTPSynchronized`, producing a TimeSyncResult tagged as
// systemd-timesyncd.
func parseTimedatectl(out string) TimeSyncResult {
	r := TimeSyncResult{Service: "systemd-timesyncd"}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "NTP":
			r.Active = val == "yes"
		case "NTPSynchronized":
			r.Synced = val == "yes"
		}
	}
	return r
}

// parseChronycTracking extracts a very coarse sync signal from chronyc output.
// chronyd running at all implies Active; "Leap status : Normal" implies
// Synced.
func parseChronycTracking(out string) TimeSyncResult {
	r := TimeSyncResult{Service: "chronyd", Active: true}
	if strings.Contains(out, "Leap status     : Normal") ||
		strings.Contains(out, "Leap status : Normal") {
		r.Synced = true
	}
	return r
}

// ─────────────────────────────────────────────────────────────────────────
// Default runner (production)
// ─────────────────────────────────────────────────────────────────────────

type defaultRunner struct {
	db   *gorm.DB
	http *http.Client
}

func newDefaultRunner(db *gorm.DB) *defaultRunner {
	return &defaultRunner{
		db: db,
		http: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

func (d *defaultRunner) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (d *defaultRunner) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (d *defaultRunner) LookPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (d *defaultRunner) Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	return cmd.CombinedOutput()
}

func (d *defaultRunner) PortFreeTCP(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func (d *defaultRunner) GetUID() int { return os.Getuid() }

func (d *defaultRunner) FetchPublicIPv4(ctx context.Context) (string, error) {
	return fetchIP(ctx, d.http, "https://api.ipify.org")
}

func (d *defaultRunner) FetchPublicIPv6(ctx context.Context) (string, error) {
	return fetchIP(ctx, d.http, "https://api6.ipify.org")
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ip service returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid ip %q", ip)
	}
	return ip, nil
}

func (d *defaultRunner) InboundPortsFromDB() ([]int, error) {
	if d.db == nil {
		return []int{}, nil
	}
	var ports []int
	if err := d.db.Table("inbounds").
		Where("deleted_at IS NULL AND enable = ?", true).
		Pluck("port", &ports).Error; err != nil {
		return nil, err
	}
	return ports, nil
}

func (d *defaultRunner) CPUCores() (int, error) {
	n, err := cpu.Counts(true)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (d *defaultRunner) MemInfo() (int64, int64, error) {
	v, err := mem.VirtualMemory()
	if err != nil {
		return 0, 0, err
	}
	s, err := mem.SwapMemory()
	if err != nil {
		return int64(v.Total), 0, nil
	}
	return int64(v.Total), int64(s.Total), nil
}

// PrimaryNIC returns the non-loopback, non-docker interface that has an IPv4
// address. Link speed is read from /sys/class/net/<iface>/speed; -1 signals
// unknown (virtual NICs and WSL often report -1 or "Invalid argument").
func (d *defaultRunner) PrimaryNIC() (string, int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", 0, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(iface.Name, "docker") || strings.HasPrefix(iface.Name, "br-") ||
			strings.HasPrefix(iface.Name, "veth") || strings.HasPrefix(iface.Name, "lo") {
			continue
		}
		addrs, _ := iface.Addrs()
		hasIPv4 := false
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				hasIPv4 = true
				break
			}
		}
		if !hasIPv4 {
			continue
		}
		speed := -1
		if data, err := os.ReadFile("/sys/class/net/" + iface.Name + "/speed"); err == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil {
				speed = n
			}
		}
		return iface.Name, speed, nil
	}
	return "", 0, fmt.Errorf("no usable NIC found")
}

// checkCakeInModules is a best-effort fallback for systems that don't expose
// /sys/module/sch_cake yet (e.g. auto-load on first use). It scans the active
// kernel's modules.builtin + modules.alias.bin for "sch_cake".
func checkCakeInModules(r Runner) bool {
	kver, err := r.Exec(context.Background(), "uname", "-r")
	if err != nil {
		return false
	}
	v := strings.TrimSpace(string(kver))
	if v == "" {
		return false
	}
	for _, path := range []string{
		"/lib/modules/" + v + "/modules.builtin",
		"/lib/modules/" + v + "/modules.dep",
	} {
		data, err := r.ReadFile(path)
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte("sch_cake")) {
			return true
		}
	}
	return false
}
