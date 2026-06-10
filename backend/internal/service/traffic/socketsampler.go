package traffic

import (
	"context"
	"encoding/json"
	"log"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
)

// socketSampleInterval is the cadence for `ss` snapshots. 10s is fine on a
// 1-vCPU box (one cheap exec) and bounds how much of a short-lived flow's bytes
// we miss between snapshots; the collector still writes on its own 30s flush.
const socketSampleInterval = 10 * time.Second

// samplerExcludeComms are infrastructure / self processes that listen on
// sockets but are NOT proxy egress programs worth logging. Everything else that
// both listens (is a server) and makes outbound connections is auto-discovered
// and labeled by its real process name — so the panel hard-codes NO
// environment-specific proxy names (no "3x-ui", "cpa", …). Operators refine this
// via the instance-map setting: comm -> friendly label, or comm -> "" to exclude.
var samplerExcludeComms = map[string]bool{
	"sshd":             true,
	"zenithpanel":      true,
	"dockerd":          true,
	"containerd":       true,
	"containerd-shim":  true,
	"systemd":          true,
	"systemd-resolve":  true,
	"systemd-resolved": true,
	"chronyd":          true,
	"ntpd":             true,
}

var (
	reSSProc     = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`)
	reSSSent     = regexp.MustCompile(`\bbytes_sent:(\d+)`)
	reSSAcked    = regexp.MustCompile(`\bbytes_acked:(\d+)`)
	reSSReceived = regexp.MustCompile(`\bbytes_received:(\d+)`)
)

type sockBytes struct{ sent, recv uint64 }

// runSocketSampler periodically snapshots established sockets via `ss`, diffs
// per-socket byte counters, auto-attributes each to the owning proxy instance,
// and feeds the deltas to the collector. Egress (to upstreams) vs return (to
// client machines) is decided by whether the local port is a listening port.
func (e *EgressCollector) runSocketSampler(ctx context.Context) {
	if _, err := exec.LookPath("ss"); err != nil {
		log.Printf("traffic egress: socket sampler disabled (ss not found: %v)", err)
		return
	}
	prev := map[string]sockBytes{}
	ticker := time.NewTicker(socketSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !getBoolSetting(SettingEgressEnabled, true) || !getBoolSetting(SettingEgressSocketSampler, true) {
				continue
			}
			prev = e.sampleSockets(prev)
		}
	}
}

// samplerContext bundles the per-tick discovery state so attribution is
// consistent across the TCP and UDP scans and the coverage discovery.
type samplerContext struct {
	listenPorts map[int]bool
	listeners   map[string]bool // comms that own a LISTEN socket (i.e. servers)
	overrides   map[string]string
	clashOn     bool
}

func (e *EgressCollector) sampleContext() samplerContext {
	ports, comms := listeningPortsAndComms()
	return samplerContext{
		listenPorts: ports,
		listeners:   comms,
		overrides:   operatorInstanceMap(),
		clashOn:     config.GetSetting("singbox_clash_api_enabled") == "true",
	}
}

// instanceFor decides the instance label for a process comm, or ("", false) to
// skip. Precedence: explicit operator override (incl "" = exclude) > infra
// denylist > "the panel's own sing-box is already covered, with domains, by the
// in-process Clash tier" > only track processes that are themselves listening
// servers (proxies), labeled by their real comm.
func (sc samplerContext) instanceFor(comm string) (string, bool) {
	if comm == "" {
		return "", false
	}
	if label, ok := sc.overrides[comm]; ok {
		if strings.TrimSpace(label) == "" {
			return "", false
		}
		return label, true
	}
	if samplerExcludeComms[comm] {
		return "", false
	}
	if comm == "sing-box" && sc.clashOn {
		return "", false
	}
	if !sc.listeners[comm] {
		return "", false // a client (curl, ssl_client, the panel's own outbound) — not an egress program
	}
	return comm, true
}

func (e *EgressCollector) sampleSockets(prev map[string]sockBytes) map[string]sockBytes {
	sc := e.sampleContext()
	cur := map[string]sockBytes{}
	// TCP carries byte counters (-i). UDP has none, but we still record the
	// destination with a hit so e.g. a WireGuard/WARP egress is visible.
	e.scanSS(prev, cur, sc, "tcp")
	e.scanSS(prev, cur, sc, "udp")
	return cur
}

func (e *EgressCollector) scanSS(prev, cur map[string]sockBytes, sc samplerContext, proto string) {
	flag := "-tnpiOH"
	if proto == "udp" {
		flag = "-unpOH"
	}
	out, err := exec.Command("ss", flag, "state", "established").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		// Column layout depends on ss version/filter: with `state established`
		// the State column is omitted, so addresses are at [2],[3]; otherwise
		// `ESTAB recvq sendq LOCAL PEER` puts them at [3],[4]. Detect by whether
		// the first field is numeric (a queue length) or a state word.
		idx := 2
		if _, err := strconv.Atoi(f[0]); err != nil {
			idx = 3
		}
		if len(f) < idx+2 {
			continue
		}
		localStr, peerStr := f[idx], f[idx+1]
		_, lport, ok1 := splitHostPort(localStr)
		pip, _, ok2 := splitHostPort(peerStr)
		if !ok1 || !ok2 {
			continue
		}
		pip = normIP(pip)
		if pip == "" || isPrivateIP(pip) {
			continue
		}
		pm := reSSProc.FindStringSubmatch(line)
		if pm == nil {
			continue
		}
		instance, ok := sc.instanceFor(pm[1])
		if !ok {
			continue
		}
		direction := "egress"
		if sc.listenPorts[lport] {
			direction = "return"
		}

		key := proto + "|" + localStr + "|" + peerStr
		var sent, recv uint64
		if proto == "tcp" {
			sent = matchUint(reSSAcked, line)
			if sent == 0 {
				sent = matchUint(reSSSent, line)
			}
			recv = matchUint(reSSReceived, line)
		}
		cur[key] = sockBytes{sent: sent, recv: recv}

		var dUp, dDown uint64
		if old, seen := prev[key]; seen {
			if sent >= old.sent {
				dUp = sent - old.sent
			}
			if recv >= old.recv {
				dDown = recv - old.recv
			}
		} else {
			dUp, dDown = sent, recv
		}
		hit := int64(0)
		if proto == "udp" || dUp > 0 || dDown > 0 {
			hit = 1
		}
		// ss sent/recv are relative to the box. On a return (inbound) socket
		// "sent" is to-client and "recv" is from-client, so swap to keep
		// BytesUp = bytes from the client and BytesDown = bytes to the client.
		if direction == "return" {
			e.Add(instance, "", "", pip, "return", int64(dDown), int64(dUp), hit)
		} else {
			e.Add(instance, "", "", pip, "egress", int64(dUp), int64(dDown), hit)
		}
	}
}

// DiscoverInstances lists the instance labels the sampler would currently
// attribute egress to, from a live scan of listening servers. Lets the coverage
// view show egress programs present on the host even before they move traffic.
// Best-effort: returns nil if ss is unavailable.
func (e *EgressCollector) DiscoverInstances() []string {
	if _, err := exec.LookPath("ss"); err != nil {
		return nil
	}
	sc := e.sampleContext()
	set := map[string]bool{}
	for comm := range sc.listeners {
		if label, ok := sc.instanceFor(comm); ok {
			set[label] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// operatorInstanceMap is the operator-configured comm->label overrides (JSON in
// the traffic_egress_instance_map setting). Empty by default — discovery is
// automatic; this only renames or excludes.
func operatorInstanceMap() map[string]string {
	m := map[string]string{}
	if raw := strings.TrimSpace(config.GetSetting(SettingEgressInstanceMap)); raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// listeningPortsAndComms returns the set of local listening ports (for inbound
// vs outbound classification) and the set of process comms that own a listening
// socket (servers — the candidates for egress tracking).
func listeningPortsAndComms() (map[int]bool, map[string]bool) {
	ports := map[int]bool{}
	comms := map[string]bool{}
	for _, flag := range []string{"-tlnpH", "-ulnpH"} {
		b, err := exec.Command("ss", flag).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			f := strings.Fields(line)
			for _, tok := range f {
				if _, port, ok := splitHostPort(tok); ok {
					ports[port] = true
				}
			}
			if pm := reSSProc.FindStringSubmatch(line); pm != nil {
				comms[pm[1]] = true
			}
		}
	}
	return ports, comms
}

func matchUint(re *regexp.Regexp, s string) uint64 {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseUint(m[1], 10, 64)
	return n
}

// splitHostPort splits "host:port" where host may be IPv4, [IPv6], or *:port.
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 || i == len(s)-1 {
		return "", 0, false
	}
	host := s[:i]
	port, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	host = strings.Trim(host, "[]")
	return host, port, true
}

func normIP(ip string) string {
	ip = strings.Trim(ip, "[]")
	ip = strings.TrimPrefix(ip, "::ffff:")
	if ip == "*" {
		return ""
	}
	return ip
}
