package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Snapshot captures the pre-state of a TuneOp so Revert can restore it.
// Files maps absolute paths to either the pre-existing file contents (when
// present) or the zero-value literal "__zenith_absent__" to mean "remove
// the file on revert." Sysctls maps runtime sysctl keys to their pre-op
// value.
type Snapshot struct {
	Op       string            `json:"op"`
	Files    map[string]string `json:"files,omitempty"`
	Sysctls  map[string]string `json:"sysctls,omitempty"`
	Commands []string          `json:"commands,omitempty"` // revert shell commands (systemctl, etc.)
}

const snapshotAbsentSentinel = "__zenith_absent__"

// TuneOp is a reversible system-level tuning operation.
type TuneOp struct {
	Name   string
	Apply  func(ctx context.Context, params map[string]string) (Snapshot, error)
	Revert func(ctx context.Context, snap Snapshot) error
}

// tuneRoot allows tests to redirect all filesystem writes beneath a temp
// directory. Production leaves it empty, meaning absolute paths land at
// their real locations.
var tuneRoot = ""

// tuneRunner allows tests to stub out exec calls.
var tuneRunner tuneExecutor = realTuneRunner{}

type tuneExecutor interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type realTuneRunner struct{}

func (realTuneRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// resolve prepends tuneRoot to a path for testability. Production config is
// tuneRoot="", in which case the original path is returned unchanged.
func resolve(path string) string {
	if tuneRoot == "" {
		return path
	}
	return filepath.Join(tuneRoot, path)
}

// ─────────────────────────────────────────────────────────────────────────
// Registry
// ─────────────────────────────────────────────────────────────────────────

var registry = map[string]TuneOp{}

func init() {
	register(TuneOp{Name: "bbr_fq", Apply: applyBBRFQ, Revert: revertFile})
	register(TuneOp{Name: "qdisc_cake", Apply: applyQdiscCake, Revert: revertFile})
	register(TuneOp{Name: "sysctl_network", Apply: applySysctlNetwork, Revert: revertFile})
	register(TuneOp{Name: "udp_buffers_large", Apply: applyUDPBuffersLarge, Revert: revertFile})
	register(TuneOp{Name: "tcp_fastopen_full", Apply: applyTFOFull, Revert: revertFile})
	register(TuneOp{Name: "systemd_nofile", Apply: applySystemdNofile, Revert: revertFile})
	register(TuneOp{Name: "time_sync_enable", Apply: applyTimeSyncEnable, Revert: revertTimeSync})
}

func register(op TuneOp) {
	registry[op.Name] = op
}

// ApplyTuneOp dispatches to a registered TuneOp. Unknown op names are an
// error rather than a silent no-op so the orchestrator's plan validation
// catches them early.
func ApplyTuneOp(ctx context.Context, name string, params map[string]string) (Snapshot, error) {
	op, ok := registry[name]
	if !ok {
		return Snapshot{}, fmt.Errorf("unknown tune op %q", name)
	}
	return op.Apply(ctx, params)
}

// RevertTuneOp dispatches to the matching op's Revert function.
func RevertTuneOp(ctx context.Context, snap Snapshot) error {
	op, ok := registry[snap.Op]
	if !ok {
		return fmt.Errorf("unknown tune op in snapshot: %q", snap.Op)
	}
	return op.Revert(ctx, snap)
}

// ─────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────

// writeDropin writes a sysctl.d / systemd drop-in file and records its
// pre-state in the snapshot. If the file didn't exist before, the snapshot
// carries the absent-sentinel so Revert removes it.
func writeDropin(snap *Snapshot, path string, content []byte, mode os.FileMode) error {
	resolved := resolve(path)
	if snap.Files == nil {
		snap.Files = map[string]string{}
	}
	if prev, err := os.ReadFile(resolved); err == nil {
		snap.Files[path] = string(prev)
	} else {
		snap.Files[path] = snapshotAbsentSentinel
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return err
	}
	return os.WriteFile(resolved, content, mode)
}

// applySysctlReload runs `sysctl -p <file>`, treating ENOENT for sysctl as
// non-fatal (dev environments that lack the binary).
func applySysctlReload(ctx context.Context, file string) error {
	_, err := tuneRunner.Run(ctx, "sysctl", "-p", resolve(file))
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		return nil
	}
	return err
}

// revertFile restores or removes every path in snap.Files. Errors on
// individual paths are accumulated; we try to revert the rest.
func revertFile(_ context.Context, snap Snapshot) error {
	var errs []string
	for path, prev := range snap.Files {
		resolved := resolve(path)
		if prev == snapshotAbsentSentinel {
			if err := os.Remove(resolved); err != nil && !os.IsNotExist(err) {
				errs = append(errs, fmt.Sprintf("remove %s: %v", path, err))
			}
			continue
		}
		if err := os.WriteFile(resolved, []byte(prev), 0o644); err != nil {
			errs = append(errs, fmt.Sprintf("restore %s: %v", path, err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────
// Concrete ops
// ─────────────────────────────────────────────────────────────────────────

const (
	pathBBRFQ          = "/etc/sysctl.d/99-zenith-bbr.conf"
	pathQdiscCake      = "/etc/sysctl.d/99-zenith-cake.conf"
	pathSysctlNetwork  = "/etc/sysctl.d/99-zenith-network.conf"
	pathUDPBuffers     = "/etc/sysctl.d/99-zenith-udpbuf.conf"
	pathTFOFull        = "/etc/sysctl.d/99-zenith-tfo.conf"
	pathSystemdNofile  = "/etc/systemd/system.conf.d/99-zenith-nofile.conf"
)

func applyBBRFQ(ctx context.Context, _ map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "bbr_fq"}
	content := []byte("# zenith: BBR + fq qdisc\nnet.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n")
	if err := writeDropin(&snap, pathBBRFQ, content, 0o644); err != nil {
		return snap, err
	}
	return snap, applySysctlReload(ctx, pathBBRFQ)
}

func applyQdiscCake(ctx context.Context, _ map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "qdisc_cake"}
	content := []byte("# zenith: cake qdisc (preferred for lossy/mobile uplinks)\nnet.core.default_qdisc=cake\nnet.ipv4.tcp_congestion_control=bbr\n")
	if err := writeDropin(&snap, pathQdiscCake, content, 0o644); err != nil {
		return snap, err
	}
	return snap, applySysctlReload(ctx, pathQdiscCake)
}

func applySysctlNetwork(ctx context.Context, _ map[string]string) (Snapshot, error) {
	// Reuse the existing networkTuningParams catalog so the two code paths
	// stay in sync. Drop-in file name differs from the legacy one to make
	// the source obvious in audits.
	snap := Snapshot{Op: "sysctl_network"}
	var b strings.Builder
	b.WriteString("# zenith: network tuning (TCP buffers, TFO, backlog, fastreuse)\n")
	for key, val := range networkTuningParams {
		fmt.Fprintf(&b, "%s=%s\n", key, val)
	}
	if err := writeDropin(&snap, pathSysctlNetwork, []byte(b.String()), 0o644); err != nil {
		return snap, err
	}
	return snap, applySysctlReload(ctx, pathSysctlNetwork)
}

func applyUDPBuffersLarge(ctx context.Context, params map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "udp_buffers_large"}
	rmem := params["rmem_max"]
	wmem := params["wmem_max"]
	if rmem == "" {
		rmem = "16777216" // 16 MiB default
	}
	if wmem == "" {
		wmem = "16777216"
	}
	content := []byte(fmt.Sprintf("# zenith: UDP buffers sized to BDP\nnet.core.rmem_max=%s\nnet.core.wmem_max=%s\nnet.core.rmem_default=%s\nnet.core.wmem_default=%s\n", rmem, wmem, rmem, wmem))
	if err := writeDropin(&snap, pathUDPBuffers, content, 0o644); err != nil {
		return snap, err
	}
	return snap, applySysctlReload(ctx, pathUDPBuffers)
}

func applyTFOFull(ctx context.Context, _ map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "tcp_fastopen_full"}
	content := []byte("# zenith: TCP Fast Open (both client and server)\nnet.ipv4.tcp_fastopen=3\n")
	if err := writeDropin(&snap, pathTFOFull, content, 0o644); err != nil {
		return snap, err
	}
	return snap, applySysctlReload(ctx, pathTFOFull)
}

func applySystemdNofile(ctx context.Context, params map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "systemd_nofile"}
	limit := params["limit"]
	if limit == "" {
		limit = "1048576"
	}
	content := []byte(fmt.Sprintf("[Manager]\nDefaultLimitNOFILE=%s\n", limit))
	if err := writeDropin(&snap, pathSystemdNofile, content, 0o644); err != nil {
		return snap, err
	}
	// systemctl daemon-reexec applies the manager change; best-effort only.
	if _, err := tuneRunner.Run(ctx, "systemctl", "daemon-reexec"); err != nil && !errors.Is(err, exec.ErrNotFound) {
		return snap, nil // not fatal — the drop-in is in place
	}
	return snap, nil
}

// applyTimeSyncEnable ensures either chronyd or systemd-timesyncd is running.
// It records which service was already active (for Revert) and which it
// started. Since enabling an NTP client is almost universally safe, the
// Revert is a best-effort stop+disable only of a service we started.
func applyTimeSyncEnable(ctx context.Context, _ map[string]string) (Snapshot, error) {
	snap := Snapshot{Op: "time_sync_enable"}
	candidates := []string{"chronyd", "systemd-timesyncd"}

	var started string
	for _, svc := range candidates {
		if !binaryExistsForService(svc) {
			continue
		}
		if isServiceActive(ctx, svc) {
			// already running — nothing to do, nothing to revert
			snap.Commands = nil
			return snap, nil
		}
	}
	for _, svc := range candidates {
		if !binaryExistsForService(svc) {
			continue
		}
		if _, err := tuneRunner.Run(ctx, "systemctl", "enable", "--now", svc); err == nil {
			started = svc
			break
		}
	}
	if started != "" {
		snap.Commands = []string{"systemctl disable --now " + started}
	}
	return snap, nil
}

func revertTimeSync(ctx context.Context, snap Snapshot) error {
	for _, cmd := range snap.Commands {
		parts := strings.Fields(cmd)
		if len(parts) == 0 {
			continue
		}
		_, _ = tuneRunner.Run(ctx, parts[0], parts[1:]...)
	}
	return nil
}

func binaryExistsForService(svc string) bool {
	switch svc {
	case "chronyd":
		_, err := exec.LookPath("chronyd")
		return err == nil
	case "systemd-timesyncd":
		// ships with systemd on Debian/Ubuntu; presence of the service unit
		// is the best cheap check.
		_, err := os.Stat("/lib/systemd/system/systemd-timesyncd.service")
		return err == nil
	}
	return false
}

func isServiceActive(ctx context.Context, svc string) bool {
	out, err := tuneRunner.Run(ctx, "systemctl", "is-active", svc)
	return err == nil && strings.TrimSpace(string(out)) == "active"
}
