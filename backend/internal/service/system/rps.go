package system

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// RPS / RFS: when a NIC exposes fewer RX queues than there are CPUs (the
// norm for virtio NICs on VPSes, e.g. 2 queues on a 4-vCPU Ampere A1),
// receive-side packet processing only ever runs on the CPUs owning those
// queues. Receive Packet Steering spreads it across every core; Receive
// Flow Steering keeps each flow on the core where its proxy goroutine runs.
// See the kernel's Documentation/networking/scaling.rst.

const (
	rpsSockFlowEntries = 32768
	pathRPSSockFlow    = "/proc/sys/net/core/rps_sock_flow_entries"
	// pathRPSState records what was applied so it can be re-applied at boot
	// (sysfs values don't survive a reboot). Relative to the panel's working
	// directory, i.e. inside the persistent data volume.
	pathRPSState = "data/tuning-rps.json"
)

type rpsState struct {
	Iface string `json:"iface"`
	Mask  string `json:"mask"`
}

// cpuMask returns the sysfs cpumask for CPUs 0..n-1: hex, 32-bit groups,
// most-significant group first, comma separated (e.g. n=4 → "f",
// n=40 → "ff,ffffffff").
func cpuMask(n int) string {
	if n <= 0 {
		return "0"
	}
	var groups []string
	for n > 0 {
		bits := n
		if bits > 32 {
			bits = 32
		}
		// Only the most significant group can be partial, so every other
		// group is a full "ffffffff" and needs no zero-padding.
		groups = append([]string{strconv.FormatUint((uint64(1)<<bits)-1, 16)}, groups...)
		n -= bits
	}
	return strings.Join(groups, ",")
}

// rxQueues lists the rx-N queue directories of iface.
func rxQueues(iface string) []string {
	entries, err := os.ReadDir(resolve(filepath.Join("/sys/class/net", iface, "queues")))
	if err != nil {
		return nil
	}
	var qs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "rx-") {
			qs = append(qs, e.Name())
		}
	}
	return qs
}

// RxQueueCount returns the number of RX queues iface exposes (0 if unknown).
func RxQueueCount(iface string) int { return len(rxQueues(iface)) }

// writeSysValue writes a sysfs/procfs value, recording the previous one in
// the snapshot so revertFile restores it.
func writeSysValue(snap *Snapshot, path, value string) error {
	resolved := resolve(path)
	prev, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	if _, seen := snap.Files[path]; !seen {
		snap.Files[path] = strings.TrimSpace(string(prev))
	}
	return os.WriteFile(resolved, []byte(value+"\n"), 0o644)
}

// applyRPSSpread enables RPS+RFS on params["iface"] across all CPUs. It is
// a no-op (empty snapshot) when the NIC already has a queue per CPU or there
// is only one CPU.
func applyRPSSpread(_ context.Context, params map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "rps_spread"}
	iface := params["iface"]
	if iface == "" || strings.ContainsAny(iface, "/.") {
		return snap, fmt.Errorf("rps_spread: invalid iface %q", iface)
	}
	cpus := runtime.NumCPU()
	if v, err := strconv.Atoi(params["cpus"]); err == nil && v > 0 {
		cpus = v
	}
	queues := rxQueues(iface)
	if cpus < 2 || len(queues) == 0 || len(queues) >= cpus {
		return snap, nil
	}
	mask := cpuMask(cpus)
	if err := writeSysValue(&snap, pathRPSSockFlow, strconv.Itoa(rpsSockFlowEntries)); err != nil {
		return snap, fmt.Errorf("rps_spread: %w", err)
	}
	perQueue := strconv.Itoa(rpsSockFlowEntries / len(queues))
	for _, q := range queues {
		base := filepath.Join("/sys/class/net", iface, "queues", q)
		if err := writeSysValue(&snap, filepath.Join(base, "rps_cpus"), mask); err != nil {
			return snap, fmt.Errorf("rps_spread: %w", err)
		}
		if err := writeSysValue(&snap, filepath.Join(base, "rps_flow_cnt"), perQueue); err != nil {
			return snap, fmt.Errorf("rps_spread: %w", err)
		}
	}
	state, _ := json.Marshal(rpsState{Iface: iface, Mask: mask})
	if err := writeDropin(&snap, pathRPSState, state, 0o600); err != nil {
		return snap, fmt.Errorf("rps_spread: persist state: %w", err)
	}
	return snap, nil
}

// ReapplyPersistentTuning restores kernel tuning that doesn't survive a host
// reboot on its own. In the Docker deployment the sysctl drop-ins written by
// Smart Deploy live inside the container, where the host's systemd-sysctl
// never reads them, and sysfs RPS masks are always volatile. Call once at
// startup; errors are logged, never fatal.
func ReapplyPersistentTuning(ctx context.Context) {
	files, _ := filepath.Glob(resolve("/etc/sysctl.d/99-zenith-*.conf"))
	for _, f := range files {
		if out, err := tuneRunner.Run(ctx, "sysctl", "-p", f); err != nil {
			log.Printf("tuning: re-apply %s: %v (%s)", f, err, strings.TrimSpace(string(out)))
		}
	}

	raw, err := os.ReadFile(resolve(pathRPSState))
	if err != nil {
		return
	}
	var st rpsState
	if json.Unmarshal(raw, &st) != nil || st.Iface == "" || strings.ContainsAny(st.Iface, "/.") {
		return
	}
	if err := os.WriteFile(resolve(pathRPSSockFlow), []byte(strconv.Itoa(rpsSockFlowEntries)+"\n"), 0o644); err != nil {
		log.Printf("tuning: re-apply RPS: %v", err)
		return
	}
	queues := rxQueues(st.Iface)
	if len(queues) == 0 {
		return
	}
	perQueue := strconv.Itoa(rpsSockFlowEntries / len(queues))
	for _, q := range queues {
		base := resolve(filepath.Join("/sys/class/net", st.Iface, "queues", q))
		_ = os.WriteFile(filepath.Join(base, "rps_cpus"), []byte(st.Mask+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(base, "rps_flow_cnt"), []byte(perQueue+"\n"), 0o644)
	}
	log.Printf("tuning: re-applied RPS on %s (mask %s)", st.Iface, st.Mask)
}
