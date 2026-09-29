package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCPUMask(t *testing.T) {
	cases := map[int]string{
		0:  "0",
		1:  "1",
		4:  "f",
		8:  "ff",
		32: "ffffffff",
		40: "ff,ffffffff",
		64: "ffffffff,ffffffff",
	}
	for n, want := range cases {
		if got := cpuMask(n); got != want {
			t.Errorf("cpuMask(%d) = %q, want %q", n, got, want)
		}
	}
}

// fakeNIC lays out /sys/class/net/<iface>/queues and the procfs knob under
// the redirected tune root, all starting at the kernel's default "0".
func fakeNIC(t *testing.T, root, iface string, rx int) {
	t.Helper()
	for i := 0; i < rx; i++ {
		q := filepath.Join(root, "sys/class/net", iface, "queues", "rx-"+string(rune('0'+i)))
		if err := os.MkdirAll(q, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"rps_cpus", "rps_flow_cnt"} {
			if err := os.WriteFile(filepath.Join(q, f), []byte("0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "sys/class/net", iface, "queues", "tx-0"), 0o755); err != nil {
		t.Fatal(err)
	}
	proc := filepath.Join(root, "proc/sys/net/core")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proc, "rps_sock_flow_entries"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestRPSSpreadAppliesAndReverts(t *testing.T) {
	root := withTuneRoot(t)
	// The state file path is relative ("data/…"); keep it inside the temp root.
	t.Chdir(root)
	fakeNIC(t, root, "eth0", 2)

	snap, err := ApplyTuneOp(context.Background(), "rps_spread", map[string]string{"iface": "eth0", "cpus": "4"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	q0 := filepath.Join(root, "sys/class/net/eth0/queues/rx-0")
	if got := readTrim(t, filepath.Join(q0, "rps_cpus")); got != "f" {
		t.Errorf("rps_cpus = %q, want f", got)
	}
	if got := readTrim(t, filepath.Join(q0, "rps_flow_cnt")); got != "16384" {
		t.Errorf("rps_flow_cnt = %q, want 16384", got)
	}
	if got := readTrim(t, filepath.Join(root, "proc/sys/net/core/rps_sock_flow_entries")); got != "32768" {
		t.Errorf("rps_sock_flow_entries = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, pathRPSState)); err != nil {
		t.Errorf("state file not written: %v", err)
	}

	if err := RevertTuneOp(context.Background(), snap); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if got := readTrim(t, filepath.Join(q0, "rps_cpus")); got != "0" {
		t.Errorf("after revert rps_cpus = %q, want 0", got)
	}
	if got := readTrim(t, filepath.Join(root, "proc/sys/net/core/rps_sock_flow_entries")); got != "0" {
		t.Errorf("after revert rps_sock_flow_entries = %q, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(root, pathRPSState)); !os.IsNotExist(err) {
		t.Errorf("state file should be removed on revert, stat err = %v", err)
	}
}

func TestRPSSpreadNoOpWhenQueuePerCPU(t *testing.T) {
	root := withTuneRoot(t)
	t.Chdir(root)
	fakeNIC(t, root, "eth0", 4)

	snap, err := ApplyTuneOp(context.Background(), "rps_spread", map[string]string{"iface": "eth0", "cpus": "4"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(snap.Files) != 0 {
		t.Errorf("expected no-op, snapshot touched %v", snap.Files)
	}
	if got := readTrim(t, filepath.Join(root, "sys/class/net/eth0/queues/rx-0/rps_cpus")); got != "0" {
		t.Errorf("rps_cpus changed to %q", got)
	}
}

func TestRPSSpreadRejectsPathInIface(t *testing.T) {
	withTuneRoot(t)
	if _, err := ApplyTuneOp(context.Background(), "rps_spread", map[string]string{"iface": "../../etc"}); err == nil {
		t.Fatal("expected error for path-like iface")
	}
}

func TestReapplyPersistentTuning(t *testing.T) {
	root := withTuneRoot(t)
	t.Chdir(root)
	fakeNIC(t, root, "eth0", 2)
	runner := tuneRunner.(*stubRunner)

	sysctlDir := filepath.Join(root, "etc/sysctl.d")
	if err := os.MkdirAll(sysctlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"99-zenith-bbr.conf", "50-other.conf"} {
		if err := os.WriteFile(filepath.Join(sysctlDir, f), []byte("x=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pathRPSState), []byte(`{"iface":"eth0","mask":"f"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ReapplyPersistentTuning(context.Background())

	if len(runner.calls) != 1 || !strings.HasSuffix(runner.calls[0], "99-zenith-bbr.conf") {
		t.Errorf("expected exactly the zenith drop-in to be reloaded, got %v", runner.calls)
	}
	if got := readTrim(t, filepath.Join(root, "sys/class/net/eth0/queues/rx-1/rps_cpus")); got != "f" {
		t.Errorf("rps_cpus not re-applied: %q", got)
	}
}
