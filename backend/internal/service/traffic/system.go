package traffic

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// systemAggregator computes NIC rates from successive gopsutil byte counters
// and enumerates processes that currently hold network sockets. Per-process
// bandwidth requires nethogs/eBPF; we expose connection count + destinations
// which is what an operator can actually act on without those dependencies.
type systemAggregator struct {
	mu      sync.Mutex
	prevNIC map[string]psnet.IOCountersStat
	prevAt  time.Time
}

func newSystemAggregator() *systemAggregator {
	return &systemAggregator{prevNIC: map[string]psnet.IOCountersStat{}}
}

// sampleNICs returns one entry per network interface with the bytes/sec rate
// since the previous call. Loopback and docker bridges are skipped to keep
// the list focused on traffic that actually crosses the public boundary.
func (s *systemAggregator) sampleNICs() ([]NICSample, error) {
	counters, err := psnet.IOCounters(true)
	if err != nil {
		return nil, fmt.Errorf("read nic counters: %w", err)
	}
	now := time.Now()
	s.mu.Lock()
	prev := s.prevNIC
	dt := now.Sub(s.prevAt).Seconds()
	next := make(map[string]psnet.IOCountersStat, len(counters))
	for _, c := range counters {
		next[c.Name] = c
	}
	s.prevNIC = next
	s.prevAt = now
	s.mu.Unlock()

	if dt <= 0 || dt > 60 {
		dt = 0
	}

	out := make([]NICSample, 0, len(counters))
	for _, c := range counters {
		if skipNIC(c.Name) {
			continue
		}
		ns := NICSample{
			Name:     c.Name,
			TotalIn:  c.BytesRecv,
			TotalOut: c.BytesSent,
		}
		if old, ok := prev[c.Name]; ok && dt > 0 {
			if c.BytesRecv >= old.BytesRecv {
				ns.InRateBps = uint64(float64(c.BytesRecv-old.BytesRecv) / dt)
			}
			if c.BytesSent >= old.BytesSent {
				ns.OutRateBps = uint64(float64(c.BytesSent-old.BytesSent) / dt)
			}
		}
		out = append(out, ns)
	}
	sort.Slice(out, func(i, j int) bool {
		ri := out[i].InRateBps + out[i].OutRateBps
		rj := out[j].InRateBps + out[j].OutRateBps
		if ri != rj {
			return ri > rj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// skipNIC filters interfaces that are noise to a typical operator: loopback,
// docker bridges, tap/tun device names that just shuffle the proxy's own
// traffic, etc. Errs on the side of inclusion when the name is unfamiliar.
func skipNIC(name string) bool {
	switch name {
	case "lo", "lo0":
		return true
	}
	for _, p := range []string{"docker", "br-", "veth", "cni", "flannel"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// sampleProcesses returns processes with active network sockets, sorted by
// connection count desc. We cap the list at processSampleCap so a noisy host
// doesn't push megabytes of JSON to the browser every five seconds.
func (s *systemAggregator) sampleProcesses() ([]ProcessSample, error) {
	const processSampleCap = 30
	procs, err := process.Processes()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	out := make([]ProcessSample, 0, 32)
	for _, p := range procs {
		conns, err := p.Connections()
		if err != nil || len(conns) == 0 {
			continue
		}
		name, _ := p.Name()
		username, _ := p.Username()
		cmd, _ := p.Cmdline()
		sample := ProcessSample{
			PID:     int(p.Pid),
			Name:    name,
			User:    username,
			Command: truncate(cmd, 200),
		}
		dests := map[string]struct{}{}
		ports := map[int]struct{}{}
		for _, c := range conns {
			switch c.Status {
			case "LISTEN":
				if c.Laddr.Port > 0 {
					ports[int(c.Laddr.Port)] = struct{}{}
				}
			default:
				if c.Raddr.IP != "" && c.Raddr.Port > 0 {
					dests[fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port)] = struct{}{}
				}
			}
		}
		sample.ActiveConns = len(conns)
		sample.ListenPorts = intsSorted(ports)
		sample.Destinations = topNStrings(dests, 5)
		out = append(out, sample)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ActiveConns != out[j].ActiveConns {
			return out[i].ActiveConns > out[j].ActiveConns
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > processSampleCap {
		out = out[:processSampleCap]
	}
	return out, nil
}

func intsSorted(set map[int]struct{}) []int {
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
