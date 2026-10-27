package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withRunnerDocker(t *testing.T, f func(context.Context, ...string) ([]byte, error)) {
	t.Helper()
	old := runRunnerDocker
	runRunnerDocker = f
	t.Cleanup(func() { runRunnerDocker = old })
}

func withExternalContainerIdentity(t *testing.T, read func(string) ([]byte, error), hostname func() (string, error)) {
	t.Helper()
	oldRead, oldHostname := readContainerIdentityFile, containerIdentityHostname
	readContainerIdentityFile, containerIdentityHostname = read, hostname
	t.Cleanup(func() { readContainerIdentityFile, containerIdentityHostname = oldRead, oldHostname })
}

func TestExternalRuntimeRequiresExplicitOptIn(t *testing.T) {
	t.Setenv(externalRuntimeEnv, "")
	if ExternalRuntimeEnabled() {
		t.Fatal("external runtime enabled without explicit setting")
	}
	t.Setenv(externalRuntimeEnv, "external")
	if !ExternalRuntimeEnabled() {
		t.Fatal("external runtime not enabled")
	}
}

func TestExternalEngineRunningChecksPidInsideRunner(t *testing.T) {
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner-1")
	var got []string
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		got = args
		return []byte("true"), nil
	})
	if !ExternalEngineRunning(context.Background(), "xray") {
		t.Fatal("expected running engine")
	}
	if len(got) < 5 || got[0] != "exec" || got[1] != "runner-1" || !strings.Contains(strings.Join(got, " "), "xray.pid") {
		t.Fatalf("unexpected docker invocation: %#v", got)
	}
}

func TestRequireExternalRuntimeRejectsStoppedRunner(t *testing.T) {
	t.Setenv(externalRuntimeEnv, "external")
	withRunnerDocker(t, func(_ context.Context, _ ...string) ([]byte, error) { return []byte("false"), nil })
	if err := RequireExternalRuntime(context.Background()); err == nil {
		t.Fatal("expected stopped runner rejection")
	}
}

func TestExternalRuntimeStartedAtIsStableRunnerEpoch(t *testing.T) {
	t.Setenv(runnerNameEnv, "runner-1")
	const raw = "2026-10-27T01:02:03.456789Z"
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) != 4 || args[0] != "inspect" || args[2] != "{{.State.StartedAt}}" || args[3] != "runner-1" {
			t.Fatalf("unexpected docker invocation: %#v", args)
		}
		return []byte(raw), nil
	})
	got, ok := ExternalRuntimeStartedAt(context.Background())
	if !ok {
		t.Fatal("expected runner epoch")
	}
	want, _ := time.Parse(time.RFC3339Nano, raw)
	if !got.Equal(want) {
		t.Fatalf("epoch = %s, want %s", got, want)
	}
}

func TestExternalTopologyRejectsSelfAndReadOnlyMounts(t *testing.T) {
	for _, tc := range []struct {
		name, panel, runner string
		ok                  bool
	}{
		{"valid", "panel-id|host|bind::/data:true", "runner-id|host|bind::/data:true", true},
		{"same container", "panel-id|host|bind::/data:true", "panel-id|host|bind::/data:true", false},
		{"read-only", "panel-id|host|bind::/data:false", "runner-id|host|bind::/data:false", false},
		{"different mount", "panel-id|host|bind::/data:true", "runner-id|host|bind::/other:true", false},
		{"bridge network", "panel-id|bridge|bind::/data:true", "runner-id|bridge|bind::/data:true", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(externalRuntimeEnv, "external")
			t.Setenv(runnerNameEnv, "runner")
			withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
				if strings.Contains(args[2], ".State.Running") {
					return []byte("true"), nil
				}
				if args[len(args)-1] == "runner" {
					return []byte(tc.runner), nil
				}
				return []byte(tc.panel), nil
			})
			err := RequireExternalRuntimeTopology(context.Background())
			if (err == nil) != tc.ok {
				t.Fatalf("got error %v; wanted success=%v", err, tc.ok)
			}
		})
	}
}

func TestExternalTopologyUsesMountinfoContainerIDBeforeHostHostname(t *testing.T) {
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	const panelID = "0123456789abcdef0123456789abcdef"
	withExternalContainerIdentity(t, func(path string) ([]byte, error) {
		if path == "/proc/self/mountinfo" {
			return []byte("42 21 0:42 /docker/containers/" + panelID + "/hostname /etc/hostname rw - ext4 /dev/sda rw\n"), nil
		}
		return nil, os.ErrNotExist
	}, func() (string, error) { return "vps-hostname", nil })
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		if strings.Contains(args[2], ".State.Running") {
			return []byte("true"), nil
		}
		if args[len(args)-1] == "runner" {
			return []byte("runner-id|host|volume:zenith-data::true"), nil
		}
		if args[len(args)-1] != panelID {
			t.Fatalf("panel inspect used %q, want mountinfo container ID", args[len(args)-1])
		}
		return []byte(panelID + "|host|volume:zenith-data::true"), nil
	})
	if err := RequireExternalRuntimeTopology(context.Background()); err != nil {
		t.Fatalf("topology rejected valid mountinfo identity: %v", err)
	}
}

func TestWriteExternalEngineMarker(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := writeExternalEngineMarker("xray", true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("data", "runtime", "xray.enabled")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := writeExternalEngineMarker("xray", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("marker still exists: %v", err)
	}
}

func withExternalRuntimeTestDir(t *testing.T) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func externalRuntimeTestTarget(config, candidate string) runtimeTarget {
	core := &BaseCore{ConfigPath: config, externalEngine: "xray"}
	return runtimeTarget{
		name:   "xray",
		wanted: true,
		engine: engineAdapter{
			core:     core,
			generate: func() (string, error) { return candidate, nil },
		},
	}
}

// mockExternalRuntimeDocker responds to the Docker calls shared by the
// external transaction: runner liveness, topology, validation, restart and
// the runner-local PID check. The configs remain real files under t.TempDir.
func mockExternalRuntimeDocker(t *testing.T, restart func(int) error, validateErr error) *int {
	t.Helper()
	restarts := 0
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "inspect":
			if strings.Contains(args[2], ".State.Running") {
				return []byte("true"), nil
			}
			if args[len(args)-1] == "runner" {
				return []byte("runner-id|host|bind::/data:true"), nil
			}
			return []byte("panel-id|host|bind::/data:true"), nil
		case "restart":
			restarts++
			if restart != nil {
				if err := restart(restarts); err != nil {
					// Docker returns useful stderr alongside a non-zero exit. Preserve it
					// so the production wrapper's error path is exercised realistically.
					return []byte(err.Error()), err
				}
			}
			return nil, nil
		case "exec":
			// Config validation invokes the core binary; PID checks invoke sh.
			if args[2] != "sh" && validateErr != nil {
				return []byte("candidate rejected"), validateErr
			}
			return []byte("true"), nil
		default:
			t.Fatalf("unexpected docker command: %#v", args)
			return nil, nil
		}
	})
	return &restarts
}

func writeExternalRuntimeFixture(t *testing.T) (config, managed string) {
	t.Helper()
	config = filepath.Join("data", "xray_config.json")
	managed = filepath.Join("data", "runtime", "external-managed")
	if err := os.MkdirAll(filepath.Dir(managed), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"old":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("old-revision\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "runtime", "xray.enabled"), []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return config, managed
}

func TestTransitionExternalRuntimeCommitsRealFiles(t *testing.T) {
	withExternalRuntimeTestDir(t)
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	config, managed := writeExternalRuntimeFixture(t)
	restarts := mockExternalRuntimeDocker(t, nil, nil)

	revision, err := transitionExternalRuntime(context.Background(), []runtimeTarget{
		externalRuntimeTestTarget(config, `{"new":true}`),
	}, nil, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if len(revision) != 64 || *restarts != 1 {
		t.Fatalf("unexpected commit revision/restarts: %q/%d", revision, *restarts)
	}
	if got, err := os.ReadFile(config); err != nil || string(got) != `{"new":true}` {
		t.Fatalf("candidate config not committed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(managed); err != nil || string(got) != revision+"\n" {
		t.Fatalf("managed revision not committed: %q, %v", got, err)
	}
}

func TestTransitionExternalRuntimePreflightFailureDoesNotRestartOrMutateFiles(t *testing.T) {
	withExternalRuntimeTestDir(t)
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	config, managed := writeExternalRuntimeFixture(t)
	restarts := mockExternalRuntimeDocker(t, nil, errors.New("invalid candidate"))

	_, err := transitionExternalRuntime(context.Background(), []runtimeTarget{
		externalRuntimeTestTarget(config, `{"new":true}`),
	}, nil, "auto")
	if err == nil || !strings.Contains(err.Error(), "preflight external xray") {
		t.Fatalf("wanted preflight failure, got %v", err)
	}
	if *restarts != 0 {
		t.Fatalf("preflight failure restarted runner %d times", *restarts)
	}
	if got, readErr := os.ReadFile(config); readErr != nil || string(got) != `{"old":true}` {
		t.Fatalf("preflight changed live config: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(managed); readErr != nil || string(got) != "old-revision\n" {
		t.Fatalf("preflight changed managed marker: %q, %v", got, readErr)
	}
}

func TestTransitionExternalRuntimeSkipsDisabledEngineValidationAndConfigWrite(t *testing.T) {
	withExternalRuntimeTestDir(t)
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	xrayConfig, _ := writeExternalRuntimeFixture(t)
	singboxConfig := filepath.Join("data", "singbox_config.json")
	if err := os.WriteFile(singboxConfig, []byte(`{"old-singbox":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "runtime", "singbox.enabled"), []byte("enabled\n"), 0600); err != nil {
		t.Fatal(err)
	}

	restarts, singboxChecks := 0, 0
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "inspect":
			if strings.Contains(args[2], ".State.Running") {
				return []byte("true"), nil
			}
			if args[len(args)-1] == "runner" {
				return []byte("runner-id|host|volume:zenith-data::true"), nil
			}
			return []byte("panel-id|host|volume:zenith-data::true"), nil
		case "restart":
			restarts++
			return nil, nil
		case "exec":
			if args[2] == "sing-box" {
				singboxChecks++
				return []byte("old sing-box rejects new schema"), errors.New("unexpected sing-box check")
			}
			return []byte("true"), nil
		default:
			t.Fatalf("unexpected docker command: %#v", args)
			return nil, nil
		}
	})

	targets := []runtimeTarget{
		externalRuntimeTestTarget(xrayConfig, `{"new-xray":true}`),
		{
			name:   "singbox",
			wanted: false,
			engine: engineAdapter{
				core:     &BaseCore{ConfigPath: singboxConfig, externalEngine: "singbox"},
				generate: func() (string, error) { return `{"new-singbox":true}`, nil },
			},
		},
	}
	if _, err := transitionExternalRuntime(context.Background(), targets, nil, "auto"); err != nil {
		t.Fatal(err)
	}
	if singboxChecks != 0 {
		t.Fatalf("disabled sing-box was validated %d time(s)", singboxChecks)
	}
	if restarts != 1 {
		t.Fatalf("runner restart count = %d, want 1", restarts)
	}
	if got, err := os.ReadFile(singboxConfig); err != nil || string(got) != `{"old-singbox":true}` {
		t.Fatalf("disabled sing-box config changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join("data", "runtime", "singbox.enabled")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("disabled sing-box marker remains: %v", err)
	}
}

func TestTransitionExternalRuntimeWantedSingboxValidationFailureLeavesOldRunnerUntouched(t *testing.T) {
	withExternalRuntimeTestDir(t)
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	xrayConfig, managed := writeExternalRuntimeFixture(t)
	singboxConfig := filepath.Join("data", "singbox_config.json")
	singboxMarker := filepath.Join("data", "runtime", "singbox.enabled")
	if err := os.WriteFile(singboxConfig, []byte(`{"old-singbox":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(singboxMarker, []byte("enabled\n"), 0600); err != nil {
		t.Fatal(err)
	}

	restarts := 0
	withRunnerDocker(t, func(_ context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "inspect":
			if strings.Contains(args[2], ".State.Running") {
				return []byte("true"), nil
			}
			if args[len(args)-1] == "runner" {
				return []byte("runner-id|host|volume:zenith-data::true"), nil
			}
			return []byte("panel-id|host|volume:zenith-data::true"), nil
		case "restart":
			restarts++
			return nil, nil
		case "exec":
			if args[2] == "sing-box" {
				return []byte("unknown field default_domain_resolver"), errors.New("candidate rejected")
			}
			return []byte("true"), nil
		default:
			t.Fatalf("unexpected docker command: %#v", args)
			return nil, nil
		}
	})

	targets := []runtimeTarget{
		externalRuntimeTestTarget(xrayConfig, `{"new-xray":true}`),
		{
			name:   "singbox",
			wanted: true,
			engine: engineAdapter{
				core:     &BaseCore{ConfigPath: singboxConfig, externalEngine: "singbox"},
				generate: func() (string, error) { return `{"new-singbox":true}`, nil },
			},
		},
	}
	if _, err := transitionExternalRuntime(context.Background(), targets, nil, "auto"); err == nil || !strings.Contains(err.Error(), "preflight external singbox") {
		t.Fatalf("wanted sing-box preflight failure, got %v", err)
	}
	if restarts != 0 {
		t.Fatalf("failed preflight restarted old runner %d time(s)", restarts)
	}
	for path, want := range map[string]string{
		xrayConfig:    `{"old":true}`,
		singboxConfig: `{"old-singbox":true}`,
		managed:       "old-revision\n",
		singboxMarker: "enabled\n",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("preflight changed %s: %q, %v", path, got, err)
		}
	}
}

func TestTransitionExternalRuntimeRestartFailureRestoresOldConfigAndMarker(t *testing.T) {
	withExternalRuntimeTestDir(t)
	t.Setenv(externalRuntimeEnv, "external")
	t.Setenv(runnerNameEnv, "runner")
	config, managed := writeExternalRuntimeFixture(t)
	singboxConfig := filepath.Join("data", "singbox_config.json")
	singboxMarker := filepath.Join("data", "runtime", "singbox.enabled")
	if err := os.WriteFile(singboxConfig, []byte(`{"old-singbox":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(singboxMarker, []byte("enabled\n"), 0600); err != nil {
		t.Fatal(err)
	}
	restarts := mockExternalRuntimeDocker(t, func(n int) error {
		if n == 1 {
			return errors.New("runner stopped during candidate restart")
		}
		return nil // the recovery restart succeeds and sees the restored PID.
	}, nil)

	_, err := transitionExternalRuntime(context.Background(), []runtimeTarget{
		externalRuntimeTestTarget(config, `{"new":true}`),
		{
			name:   "singbox",
			wanted: false,
			engine: engineAdapter{
				core:     &BaseCore{ConfigPath: singboxConfig, externalEngine: "singbox"},
				generate: func() (string, error) { return `{"new-singbox":true}`, nil },
			},
		},
	}, nil, "auto")
	if err == nil || !strings.Contains(err.Error(), "runner stopped during candidate restart") || !strings.Contains(err.Error(), "rollback_unverified") {
		t.Fatalf("wanted candidate error plus explicit rollback qualification, got %v", err)
	}
	if *restarts != 2 {
		t.Fatalf("wanted candidate and recovery restart, got %d", *restarts)
	}
	if got, readErr := os.ReadFile(config); readErr != nil || string(got) != `{"old":true}` {
		t.Fatalf("rollback did not restore config: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(managed); readErr != nil || string(got) != "old-revision\n" {
		t.Fatalf("rollback did not restore managed marker: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(singboxConfig); readErr != nil || string(got) != `{"old-singbox":true}` {
		t.Fatalf("rollback did not preserve disabled sing-box config: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(singboxMarker); readErr != nil || string(got) != "enabled\n" {
		t.Fatalf("rollback did not restore disabled sing-box marker: %q, %v", got, readErr)
	}
}
