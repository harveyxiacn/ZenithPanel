package proxy

// External proxy runtime support intentionally requires an explicit opt-in.
// It is for installations that run Xray and sing-box in a separate, long-lived
// container while the panel container is replaced during an OTA update. The
// panel must never silently fall back to local engines: doing so would create
// a second listener on the same host port and make the update unsafe.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
)

const (
	externalRuntimeEnv      = "ZENITH_PROXY_RUNTIME"
	runnerNameEnv           = "ZENITH_PROXY_RUNNER_CONTAINER"
	defaultRunnerName       = "zenith-proxy"
	externalRevisionSetting = "external_runtime_revision"
)

var dockerContainerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// runRunnerDocker is a test seam. Production panels use the Docker CLI already
// bundled in the image and its mounted /var/run/docker.sock.
var runRunnerDocker = func(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", args...).CombinedOutput()
}

func ExternalRuntimeEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(externalRuntimeEnv)), "external")
}

func ExternalRunnerName() (string, error) {
	name := strings.TrimSpace(os.Getenv(runnerNameEnv))
	if name == "" {
		name = defaultRunnerName
	}
	if !dockerContainerName.MatchString(name) {
		return "", fmt.Errorf("invalid %s %q", runnerNameEnv, name)
	}
	return name, nil
}

func runnerCommand(ctx context.Context) ([]byte, error) {
	name, err := ExternalRunnerName()
	if err != nil {
		return nil, err
	}
	return runRunnerDocker(ctx, "inspect", "--format", "{{if .State.Running}}true{{else}}false{{end}}", name)
}

// ExternalRuntimeRunning checks the runner container, not a local proxy PID.
func ExternalRuntimeRunning(ctx context.Context) bool {
	out, err := runnerCommand(ctx)
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// ExternalRuntimeStartedAt returns the runner container epoch. It is stable
// across panel replacements, unlike deriving it from a Docker inspect round
// trip and time.Now at separate instants.
func ExternalRuntimeStartedAt(ctx context.Context) (time.Time, bool) {
	name, err := ExternalRunnerName()
	if err != nil {
		return time.Time{}, false
	}
	out, err := runRunnerDocker(ctx, "inspect", "--format", "{{.State.StartedAt}}", name)
	if err != nil {
		return time.Time{}, false
	}
	started, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
	if err != nil || started.IsZero() {
		return time.Time{}, false
	}
	return started, true
}

// ExternalRuntimeUptime is the runner's observed process lifetime. The
// external runner starts its enabled engines together, so this is a useful
// engine uptime only when the corresponding engine pid is still alive.
func ExternalRuntimeUptime(ctx context.Context) (time.Duration, bool) {
	started, ok := ExternalRuntimeStartedAt(ctx)
	if !ok {
		return 0, false
	}
	return time.Since(started), true
}

// ExternalEngineRunning checks the runner's pidfile from inside its own PID
// namespace. A running container alone is insufficient because it can have
// failed to launch one of its cores.
func ExternalEngineRunning(ctx context.Context, engine string) bool {
	if engine != "xray" && engine != "singbox" {
		return false
	}
	name, err := ExternalRunnerName()
	if err != nil {
		return false
	}
	pidFile := "/opt/zenithpanel/data/runtime/" + engine + ".pid"
	out, err := runRunnerDocker(ctx, "exec", name, "sh", "-c", "test -s "+pidFile+" && kill -0 \"$(cat "+pidFile+")\" && printf true")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// RequireExternalRuntime is used before any action that would replace the
// panel. It deliberately does not start or repair the runner.
func RequireExternalRuntime(ctx context.Context) error {
	if !ExternalRuntimeEnabled() {
		return nil
	}
	name, err := ExternalRunnerName()
	if err != nil {
		return err
	}
	if !ExternalRuntimeRunning(ctx) {
		return fmt.Errorf("external proxy runner %q is not running; refusing an update that could create duplicate listeners", name)
	}
	return nil
}

// RequireExternalRuntimeTopology verifies the two containers share host
// networking and the same writable data mount before Apply writes configs.
// Hostname is the Docker container identifier for supported panel deployments;
// a custom hostname that cannot be inspected fails closed rather than risking a
// runner restart against a different volume.
func RequireExternalRuntimeTopology(ctx context.Context) error {
	if err := RequireExternalRuntime(ctx); err != nil || !ExternalRuntimeEnabled() {
		return err
	}
	panelID, err := os.Hostname()
	if err != nil || strings.TrimSpace(panelID) == "" {
		return fmt.Errorf("determine panel container identity for external runtime")
	}
	runner, err := ExternalRunnerName()
	if err != nil {
		return err
	}
	const format = "{{.Id}}|{{.HostConfig.NetworkMode}}|{{range .Mounts}}{{if eq .Destination \"/opt/zenithpanel/data\"}}{{.Type}}:{{.Name}}:{{.Source}}:{{.RW}}{{end}}{{end}}"
	panel, panelErr := runRunnerDocker(ctx, "inspect", "--format", format, panelID)
	runnerInfo, runnerErr := runRunnerDocker(ctx, "inspect", "--format", format, runner)
	if panelErr != nil || runnerErr != nil {
		return fmt.Errorf("inspect panel/runner topology for external runtime")
	}
	panelFields := strings.SplitN(strings.TrimSpace(string(panel)), "|", 2)
	runnerFields := strings.SplitN(strings.TrimSpace(string(runnerInfo)), "|", 2)
	if len(panelFields) != 2 || len(runnerFields) != 2 || panelFields[0] == "" || runnerFields[0] == "" || panelFields[0] == runnerFields[0] {
		return fmt.Errorf("external proxy runner must be a separate container")
	}
	panelSig, runnerSig := panelFields[1], runnerFields[1]
	if !strings.HasPrefix(panelSig, "host|") || !strings.HasSuffix(panelSig, ":true") || panelSig != runnerSig {
		return fmt.Errorf("external proxy runner must share host networking and the panel's writable /opt/zenithpanel/data mount")
	}
	return nil
}

// RequireExternalRuntimeReady additionally verifies that every engine needed
// by the persisted enabled inbounds is alive in the runner. It is used before
// replacing a panel container, while the old panel is still available.
func RequireExternalRuntimeReady(ctx context.Context) error {
	if err := RequireExternalRuntime(ctx); err != nil || !ExternalRuntimeEnabled() {
		return err
	}
	marker, err := os.ReadFile(filepath.Join("data", "runtime", "external-managed"))
	if err != nil {
		return fmt.Errorf("external proxy runner is not initialized by a verified panel apply: %w", err)
	}
	revision := strings.TrimSpace(string(marker))
	if revision == "" || revision != config.GetSetting(externalRevisionSetting) {
		return fmt.Errorf("external proxy runner revision does not match persisted panel state; run proxy apply before OTA")
	}
	var inbounds []model.Inbound
	if err := config.ReadDB().WithContext(ctx).Where("enable = ?", true).Find(&inbounds).Error; err != nil {
		return fmt.Errorf("read enabled inbounds for external runtime: %w", err)
	}
	mode := config.GetSetting(runtimeModeSetting)
	if mode == "" {
		mode = "auto"
	}
	wantXray, wantSingbox := false, false
	for _, in := range inbounds {
		switch mode {
		case "singbox":
			wantSingbox = true
		case "xray":
			if IsXraySupported(in.Protocol) {
				wantXray = true
			}
		case "auto":
			if IsXraySupported(in.Protocol) {
				wantXray = true
			} else {
				wantSingbox = true
			}
		default:
			return fmt.Errorf("invalid persisted proxy runtime mode %q", mode)
		}
	}
	if wantXray && !ExternalEngineRunning(ctx, "xray") {
		return fmt.Errorf("external proxy runner is missing a running xray engine")
	}
	if wantSingbox && !ExternalEngineRunning(ctx, "singbox") {
		return fmt.Errorf("external proxy runner is missing a running sing-box engine")
	}
	return nil
}

func RestartExternalRuntime(ctx context.Context) error {
	if !ExternalRuntimeEnabled() {
		return fmt.Errorf("external proxy runtime is not enabled")
	}
	name, err := ExternalRunnerName()
	if err != nil {
		return err
	}
	out, err := runRunnerDocker(ctx, "restart", name)
	if err != nil {
		return fmt.Errorf("restart external proxy runner %q: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

// ValidateExternalConfig asks the runner's own binary to validate a temporary
// shared-volume config. A newer panel image must not validate only with its
// bundled core, then restart an older runner into an incompatible config.
func ValidateExternalConfig(ctx context.Context, engine, path string) error {
	name, err := ExternalRunnerName()
	if err != nil {
		return err
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(clean, "data/") {
		return fmt.Errorf("external config %q is outside the shared data directory", path)
	}
	runnerPath := "/opt/zenithpanel/" + clean
	var args []string
	switch engine {
	case "xray":
		args = []string{"exec", name, "xray", "run", "-test", "-c", runnerPath}
	case "singbox":
		args = []string{"exec", name, "sing-box", "check", "-c", runnerPath}
	default:
		return fmt.Errorf("unknown external engine %q", engine)
	}
	out, err := runRunnerDocker(ctx, args...)
	if err != nil {
		message := strings.TrimSpace(string(out))
		if len(message) > 800 {
			message = message[len(message)-800:]
		}
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("external %s config validation failed: %s", engine, message)
	}
	return nil
}
