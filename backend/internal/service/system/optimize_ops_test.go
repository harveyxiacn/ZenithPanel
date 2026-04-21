package system

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTuneRoot temporarily redirects sysctl.d / systemd drop-in writes
// beneath a temp directory so tests can apply real ops without touching /etc.
// It also stubs out the exec runner to avoid shelling out to sysctl or
// systemctl (neither is guaranteed on every CI runner).
func withTuneRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origRoot := tuneRoot
	origRunner := tuneRunner
	tuneRoot = root
	tuneRunner = &stubRunner{}
	t.Cleanup(func() {
		tuneRoot = origRoot
		tuneRunner = origRunner
	})
	return root
}

type stubRunner struct {
	calls []string
	fail  map[string]error
}

func (s *stubRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	s.calls = append(s.calls, call)
	if err, ok := s.fail[call]; ok {
		return nil, err
	}
	return nil, nil
}

func TestApplyTuneOpUnknownName(t *testing.T) {
	_, err := ApplyTuneOp(context.Background(), "not_a_real_op", nil)
	if err == nil {
		t.Fatalf("expected error for unknown op")
	}
}

func TestApplyBBRFQWritesDropinAndAllowsRevert(t *testing.T) {
	root := withTuneRoot(t)
	snap, err := ApplyTuneOp(context.Background(), "bbr_fq", nil)
	if err != nil {
		t.Fatalf("Apply bbr_fq: %v", err)
	}
	path := filepath.Join(root, pathBBRFQ)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected drop-in at %s, got %v", path, err)
	}
	if !strings.Contains(string(data), "tcp_congestion_control=bbr") {
		t.Errorf("drop-in missing BBR line: %s", data)
	}

	// Revert removes the file.
	if err := RevertTuneOp(context.Background(), snap); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected file removed after revert, stat err = %v", err)
	}
}

func TestApplyBBRFQRevertRestoresPreExistingContent(t *testing.T) {
	root := withTuneRoot(t)
	// Pre-seed the file with older content.
	prePath := filepath.Join(root, pathBBRFQ)
	if err := os.MkdirAll(filepath.Dir(prePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(prePath, []byte("# older content\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	snap, err := ApplyTuneOp(context.Background(), "bbr_fq", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// After Apply the file should have BBR content.
	data, _ := os.ReadFile(prePath)
	if !strings.Contains(string(data), "tcp_congestion_control=bbr") {
		t.Errorf("post-apply missing BBR line: %s", data)
	}

	// Revert restores the original content.
	if err := RevertTuneOp(context.Background(), snap); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	data, _ = os.ReadFile(prePath)
	if string(data) != "# older content\n" {
		t.Errorf("expected original content after revert, got %q", data)
	}
}

func TestApplyUDPBuffersLargeHonorsParams(t *testing.T) {
	root := withTuneRoot(t)
	_, err := ApplyTuneOp(context.Background(), "udp_buffers_large", map[string]string{
		"rmem_max": "33554432",
		"wmem_max": "33554432",
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pathUDPBuffers))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	if !strings.Contains(string(data), "rmem_max=33554432") {
		t.Errorf("drop-in missing rmem_max param: %s", data)
	}
}

func TestApplyUDPBuffersLargeDefaultsWhenParamsMissing(t *testing.T) {
	root := withTuneRoot(t)
	_, err := ApplyTuneOp(context.Background(), "udp_buffers_large", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pathUDPBuffers))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	if !strings.Contains(string(data), "rmem_max=16777216") {
		t.Errorf("expected 16 MiB default, got %s", data)
	}
}

func TestApplyTFOFullRevertsCleanly(t *testing.T) {
	root := withTuneRoot(t)
	snap, err := ApplyTuneOp(context.Background(), "tcp_fastopen_full", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	path := filepath.Join(root, pathTFOFull)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected drop-in written: %v", err)
	}
	if err := RevertTuneOp(context.Background(), snap); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected drop-in removed, stat err = %v", err)
	}
}

func TestApplySystemdNofileDropsToManagerSection(t *testing.T) {
	root := withTuneRoot(t)
	_, err := ApplyTuneOp(context.Background(), "systemd_nofile", map[string]string{"limit": "2000000"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pathSystemdNofile))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	if !strings.Contains(string(data), "DefaultLimitNOFILE=2000000") {
		t.Errorf("drop-in missing requested limit: %s", data)
	}
	if !strings.Contains(string(data), "[Manager]") {
		t.Errorf("drop-in missing [Manager] section: %s", data)
	}
}

func TestApplyQdiscCakeWritesCakeNotFQ(t *testing.T) {
	root := withTuneRoot(t)
	if _, err := ApplyTuneOp(context.Background(), "qdisc_cake", nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pathQdiscCake))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	if !strings.Contains(string(data), "default_qdisc=cake") {
		t.Errorf("drop-in not cake: %s", data)
	}
}

func TestApplySysctlNetworkIncludesExistingParams(t *testing.T) {
	root := withTuneRoot(t)
	if _, err := ApplyTuneOp(context.Background(), "sysctl_network", nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, pathSysctlNetwork))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	for key := range networkTuningParams {
		if !strings.Contains(string(data), key) {
			t.Errorf("drop-in missing key %q: %s", key, data)
		}
	}
}

func TestApplyTimeSyncAlreadyActiveIsNoop(t *testing.T) {
	withTuneRoot(t)
	// Replace stub with one that reports "active" for every is-active query.
	tuneRunner = &activeRunner{}

	snap, err := ApplyTuneOp(context.Background(), "time_sync_enable", nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(snap.Commands) != 0 {
		t.Errorf("expected no revert commands when service is already active, got %v", snap.Commands)
	}
}

// activeRunner reports "active" for is-active queries.
type activeRunner struct{ calls []string }

func (r *activeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if name == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
		return []byte("active\n"), nil
	}
	return nil, nil
}
