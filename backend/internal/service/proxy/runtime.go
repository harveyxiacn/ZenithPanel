package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/harveyxiacn/ZenithPanel/backend/internal/config"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/model"
	"github.com/harveyxiacn/ZenithPanel/backend/internal/service/diagnostic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// One coordinator owns the complete two-engine transition, including rollback.
// Waiting callers can cancel before they acquire it; no worker is left queued
// after the originating request has gone away.
var runtimeGate = make(chan struct{}, 1)

const runtimeModeSetting = "proxy_runtime_mode"

// PersistedDualMode is deliberately conservative for external runners. A
// panel restart cannot inspect a runner's historical partitioning, so only a
// successfully applied, persisted "auto" mode is reported as dual.
func PersistedDualMode() bool {
	return config.GetSetting(runtimeModeSetting) == "auto"
}

func persistRuntimeMode(mode string) error {
	if err := config.SetSetting(runtimeModeSetting, mode); err != nil {
		return err
	}
	return config.SetSetting("last_apply_unix", strconv.FormatInt(time.Now().Unix(), 10))
}

// persistExternalRuntimeRevision commits the externally active runtime's
// complete identity atomically. A partial mode/revision write could otherwise
// make a live runner look safe for OTA even though the database describes a
// different configuration.
func persistExternalRuntimeRevision(ctx context.Context, mode, revision string) error {
	if config.DB == nil {
		return errors.New("settings database is unavailable")
	}
	values := []model.Setting{
		{Key: runtimeModeSetting, Value: mode},
		{Key: externalRevisionSetting, Value: revision},
		{Key: "last_apply_unix", Value: strconv.FormatInt(time.Now().Unix(), 10)},
	}
	return config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, setting := range values {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).Create(&setting).Error; err != nil {
				return fmt.Errorf("persist setting %q: %w", setting.Key, err)
			}
		}
		return nil
	})
}

type RuntimeStatus struct {
	State      string `json:"state"`
	Mode       string `json:"mode"`
	Revision   string `json:"revision,omitempty"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	RolledBack bool   `json:"rolled_back"`
}

func RuntimeStatusFor(xm *XrayManager) RuntimeStatus {
	if xm == nil {
		return RuntimeStatus{State: "idle"}
	}
	xm.mu.RLock()
	defer xm.mu.RUnlock()
	state := xm.runtimeState
	if state.State == "" {
		state.State = "idle"
	}
	return state
}

func setRuntimeStatus(xm *XrayManager, state RuntimeStatus) {
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	xm.mu.Lock()
	xm.runtimeState = state
	xm.mu.Unlock()
}

type engineSnapshot struct {
	config  string
	exists  bool
	running bool
	dual    bool
}

// This seam makes the transaction testable without relying on installed cores.
type runtimeEngine interface {
	snapshot() (engineSnapshot, error)
	prepare(context.Context, bool) (string, error)
	stop() error
	start(string, bool) error
	restore(engineSnapshot) error
}

type engineAdapter struct {
	core        *BaseCore
	generate    func() (string, error)
	stopManager func() error
}

func (e engineAdapter) snapshot() (engineSnapshot, error) {
	s := engineSnapshot{running: e.core.Status(), dual: e.core.IsDualMode()}
	b, err := os.ReadFile(e.core.ConfigPath)
	if err != nil && (!os.IsNotExist(err) || s.running) {
		return s, err
	}
	s.config, s.exists = string(b), err == nil
	return s, nil
}

func (e engineAdapter) prepare(ctx context.Context, dual bool) (string, error) {
	oldMode := e.core.IsDualMode()
	e.core.SetDualMode(dual)
	defer e.core.SetDualMode(oldMode)
	candidate, err := e.generate()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(e.core.ConfigPath), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(e.core.ConfigPath), ".candidate-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(candidate); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := e.core.validateConfigFile(ctx, f.Name()); err != nil {
		return "", err
	}
	return candidate, nil
}

func (e engineAdapter) stop() error {
	if e.stopManager != nil {
		return e.stopManager()
	}
	return e.core.Stop()
}

func (e engineAdapter) start(candidate string, dual bool) error {
	if e.core.Status() {
		return errors.New("engine still running after stop")
	}
	if err := WriteConfigToFile(e.core.ConfigPath, candidate); err != nil {
		return err
	}
	e.core.SetDualMode(dual)
	return e.core.startAndVerify(exec.Command(e.core.BinaryPath, "run", "-c", e.core.ConfigPath))
}

func (e engineAdapter) restore(s engineSnapshot) error {
	if err := e.stop(); err != nil {
		return err
	}
	e.core.SetDualMode(s.dual)
	if !s.exists {
		if err := os.Remove(e.core.ConfigPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if s.running {
		return e.start(s.config, s.dual)
	}
	return WriteConfigToFile(e.core.ConfigPath, s.config)
}

type runtimeTarget struct {
	name      string
	engine    runtimeEngine
	wanted    bool
	dual      bool
	previous  engineSnapshot
	candidate string
	unchanged bool
	force     bool
}

type externalFileSnapshot struct {
	path   string
	data   []byte
	exists bool
}

func prepareExternalCandidate(ctx context.Context, name string, adapter engineAdapter, dual bool) (string, error) {
	oldMode := adapter.core.IsDualMode()
	adapter.core.SetDualMode(dual)
	defer adapter.core.SetDualMode(oldMode)
	candidate, err := adapter.generate()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(adapter.core.ConfigPath), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(adapter.core.ConfigPath), ".runner-candidate-*.json")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(candidate); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := ValidateExternalConfig(ctx, name, path); err != nil {
		return "", err
	}
	return candidate, nil
}

func snapshotExternalFile(path string) (externalFileSnapshot, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return externalFileSnapshot{path: path}, nil
	}
	if err != nil {
		return externalFileSnapshot{}, err
	}
	return externalFileSnapshot{path: path, data: b, exists: true}, nil
}

func restoreExternalFile(s externalFileSnapshot) error {
	if !s.exists {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	return os.WriteFile(s.path, s.data, 0600)
}

// transitionRuntime validates every candidate before stopping any live engine.
// Once switching starts, failure restores the exact previous files and modes,
// never regenerating the old version from already-modified database rows.
func transitionRuntime(ctx context.Context, targets []runtimeTarget, verify func() error) (revision string, rolledBack bool, err error) {
	hash := sha256.New()
	for i := range targets {
		t := &targets[i]
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		t.previous, err = t.engine.snapshot()
		if err != nil {
			return "", false, fmt.Errorf("snapshot %s: %w", t.name, err)
		}
		if t.wanted {
			t.candidate, err = t.engine.prepare(ctx, t.dual)
			if err != nil {
				return "", false, fmt.Errorf("preflight %s: %w", t.name, err)
			}
		}
		t.unchanged = !t.force && t.previous.running == t.wanted && (!t.wanted || t.previous.exists && t.previous.config == t.candidate && t.previous.dual == t.dual)
		_, _ = fmt.Fprintf(hash, "%s:%t:%t:%s\n", t.name, t.wanted, t.dual, t.candidate)
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	revision = hex.EncodeToString(hash.Sum(nil))
	defer func() {
		if err == nil {
			return
		}
		var restoreErrors []error
		// Release all candidate ports before starting either old engine.
		for _, t := range targets {
			if t.unchanged {
				continue
			}
			if stopErr := t.engine.stop(); stopErr != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("rollback stop %s: %w", t.name, stopErr))
			}
		}
		for _, t := range targets {
			if t.unchanged {
				continue
			}
			if restoreErr := t.engine.restore(t.previous); restoreErr != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("rollback %s: %w", t.name, restoreErr))
			}
		}
		rolledBack = len(restoreErrors) == 0
		err = errors.Join(append([]error{err}, restoreErrors...)...)
	}()
	for _, t := range targets {
		if t.unchanged {
			continue
		}
		if err = t.engine.stop(); err != nil {
			return revision, false, fmt.Errorf("stop %s: %w", t.name, err)
		}
	}
	for _, t := range targets {
		if !t.wanted || t.unchanged {
			continue
		}
		if err = t.engine.start(t.candidate, t.dual); err != nil {
			return revision, false, fmt.Errorf("start %s: %w", t.name, err)
		}
	}
	if verify != nil {
		err = verify()
	}
	return revision, false, err
}

// transitionExternalRuntime prepares both engine configurations in the panel,
// then asks the separately managed runner to restart. It is intentionally not a
// rolling core upgrade: changing proxy config restarts the runner and can drop
// connections. Its purpose is to keep the runner untouched during panel-only
// container updates.
func transitionExternalRuntime(ctx context.Context, targets []runtimeTarget, enabled []model.Inbound, mode string) (string, error) {
	if err := RequireExternalRuntimeTopology(ctx); err != nil {
		return "", err
	}
	hash := sha256.New()
	// Mode is part of runtime identity even if two modes happen to generate
	// identical files; readiness must not accept an old mode's revision.
	_, _ = fmt.Fprintf(hash, "mode:%s\n", mode)
	snapshots := make([]externalFileSnapshot, 0, len(targets)*2+1)
	managedPath := filepath.Join("data", "runtime", "external-managed")
	managedSnapshot, err := snapshotExternalFile(managedPath)
	if err != nil {
		return "", fmt.Errorf("snapshot external runtime file %q: %w", managedPath, err)
	}
	snapshots = append(snapshots, managedSnapshot)
	for _, t := range targets {
		adapter, ok := t.engine.(engineAdapter)
		if !ok {
			return "", fmt.Errorf("external runtime does not support %s adapter", t.name)
		}
		for _, path := range []string{adapter.core.ConfigPath, filepath.Join("data", "runtime", t.name+".enabled")} {
			s, err := snapshotExternalFile(path)
			if err != nil {
				return "", fmt.Errorf("snapshot external runtime file %q: %w", path, err)
			}
			snapshots = append(snapshots, s)
		}
	}
	restore := func(cause error, restart bool) error {
		var errs []error
		for _, s := range snapshots {
			if err := restoreExternalFile(s); err != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", s.path, err))
			}
		}
		if restart && len(errs) == 0 {
			recoveryCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := RestartExternalRuntime(recoveryCtx); err != nil {
				errs = append(errs, fmt.Errorf("restart restored external runtime: %w", err))
			} else if err := verifyRestoredExternalRuntime(recoveryCtx, snapshots); err != nil {
				errs = append(errs, fmt.Errorf("rollback_failed: restored external runtime is not ready: %w", err))
			} else {
				// The database describes the requested (new) state, not necessarily
				// the pre-apply state. We snapshot configs and markers, but not the
				// old inbound rows, so asserting listener probes here would be a
				// false success. Surface that limitation to the operator instead.
				errs = append(errs, errors.New("rollback_unverified: restored old engine markers, but old listener readiness cannot be verified without a historic inbound snapshot"))
			}
		}
		if len(errs) == 0 {
			return cause
		}
		return errors.Join(append([]error{cause}, errs...)...)
	}
	for i := range targets {
		t := &targets[i]
		adapter, ok := t.engine.(engineAdapter)
		if !ok {
			return "", fmt.Errorf("external runtime does not support %s adapter", t.name)
		}
		candidate, err := prepareExternalCandidate(ctx, t.name, adapter, t.dual)
		if err != nil {
			return "", fmt.Errorf("preflight external %s: %w", t.name, err)
		}
		t.candidate = candidate
		if _, err := fmt.Fprintf(hash, "%s:%t:%s\n", t.name, t.wanted, candidate); err != nil {
			return "", err
		}
	}
	revision := hex.EncodeToString(hash.Sum(nil))
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return "", restore(err, false)
		}
		adapter, ok := t.engine.(engineAdapter)
		if !ok {
			return "", fmt.Errorf("external runtime does not support %s adapter", t.name)
		}
		if err := WriteConfigToFile(adapter.core.ConfigPath, t.candidate); err != nil {
			return "", restore(fmt.Errorf("write external %s config: %w", t.name, err), false)
		}
		if err := writeExternalEngineMarker(t.name, t.wanted); err != nil {
			return "", restore(err, false)
		}
	}
	if err := os.WriteFile(managedPath, []byte(revision+"\n"), 0600); err != nil {
		return "", restore(fmt.Errorf("mark external runtime managed: %w", err), false)
	}
	if err := RestartExternalRuntime(ctx); err != nil {
		return "", restore(err, true)
	}
	if err := verifyExternalRuntime(ctx, targets, enabled, mode); err != nil {
		return "", restore(err, true)
	}
	return revision, nil
}

// verifyRestoredExternalRuntime only verifies the old engine set recorded in
// the file snapshot. It deliberately does not use the new mode, new targets,
// or current database inbounds: those may describe the failed candidate, not
// the configuration just restored from disk.
func verifyRestoredExternalRuntime(ctx context.Context, snapshots []externalFileSnapshot) error {
	wanted := map[string]bool{}
	for _, snapshot := range snapshots {
		for _, engine := range []string{"xray", "singbox"} {
			if snapshot.path == filepath.Join("data", "runtime", engine+".enabled") && snapshot.exists {
				wanted[engine] = true
			}
		}
	}
	for engine := range wanted {
		if !waitForExternalEngine(ctx, engine) {
			return fmt.Errorf("restored %s engine did not become ready", engine)
		}
	}
	return nil
}

func verifyExternalRuntime(ctx context.Context, targets []runtimeTarget, enabled []model.Inbound, mode string) error {
	for _, t := range targets {
		if t.wanted && !waitForExternalEngine(ctx, t.name) {
			return fmt.Errorf("external %s did not start after configuration update", t.name)
		}
	}
	for _, in := range enabled {
		if err := ctx.Err(); err != nil {
			return err
		}
		if mode == "xray" && !IsXraySupported(in.Protocol) {
			continue
		}
		if result := diagnostic.ProbeInbound(in); !result.OK {
			return fmt.Errorf("inbound %q failed at %s: %s", in.Tag, result.Stage, result.Err)
		}
	}
	return nil
}

func waitForExternalEngine(ctx context.Context, engine string) bool {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if ExternalEngineRunning(ctx, engine) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func writeExternalEngineMarker(engine string, wanted bool) error {
	if engine != "xray" && engine != "singbox" {
		return fmt.Errorf("unknown external engine %q", engine)
	}
	path := filepath.Join("data", "runtime", engine+".enabled")
	if !wanted {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("disable external %s: %w", engine, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("enabled\n"), 0600)
}

func ApplyRuntime(ctx context.Context, xm *XrayManager, sm *SingboxManager, mode string, force ...bool) error {
	if xm == nil || sm == nil {
		return errors.New("proxy runtime is unavailable")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	if mode == "sing-box" {
		mode = "singbox"
	}
	if mode != "auto" && mode != "xray" && mode != "singbox" {
		return fmt.Errorf("unknown engine %q", mode)
	}
	select {
	case runtimeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-runtimeGate }()
	previous := RuntimeStatusFor(xm)
	state := RuntimeStatus{State: "applying", Mode: mode, Revision: previous.Revision}
	setRuntimeStatus(xm, state)
	var enabled []model.Inbound
	if err := config.DB.WithContext(ctx).Where("enable = ?", true).Find(&enabled).Error; err != nil {
		state.State, state.Error = "failed", err.Error()
		setRuntimeStatus(xm, state)
		return err
	}
	wantXray, wantSingbox := false, false
	for _, in := range enabled {
		if mode == "singbox" {
			wantSingbox = true
		} else if IsXraySupported(in.Protocol) {
			wantXray = true
		} else if mode == "auto" {
			wantSingbox = true
		}
	}
	targets := []runtimeTarget{
		{name: "xray", engine: engineAdapter{core: &xm.BaseCore, generate: xm.GenerateConfig, stopManager: xm.Stop}, wanted: wantXray, dual: mode == "auto"},
		{name: "singbox", engine: engineAdapter{core: &sm.BaseCore, generate: sm.GenerateConfig, stopManager: sm.Stop}, wanted: wantSingbox, dual: mode == "auto"},
	}
	if len(force) > 0 && force[0] {
		for i := range targets {
			targets[i].force = true
		}
	}
	if ExternalRuntimeEnabled() {
		revision, err := transitionExternalRuntime(ctx, targets, enabled, mode)
		if err != nil {
			state.State, state.Error = "failed", err.Error()
			setRuntimeStatus(xm, state)
			return err
		}
		xm.SetDualMode(mode == "auto")
		sm.SetDualMode(mode == "auto")
		if err := persistExternalRuntimeRevision(ctx, mode, revision); err != nil {
			// The runner has already accepted this revision. Delete the readiness
			// marker so RequireExternalRuntimeReady rejects OTA until a later,
			// successful apply atomically records the matching revision. We do not
			// restart again here: without a historic inbound snapshot, a second
			// restart could only claim an unverified rollback.
			quarantineErr := os.Remove(filepath.Join("data", "runtime", "external-managed"))
			if quarantineErr != nil && !os.IsNotExist(quarantineErr) {
				err = errors.Join(err, fmt.Errorf("quarantine external runtime readiness marker: %w", quarantineErr))
			}
			state.State, state.Revision = "applied_unpersisted", revision
			state.Error = fmt.Sprintf("external runtime is serving this revision, but panel persistence failed; OTA is blocked until proxy apply succeeds: %v", err)
			setRuntimeStatus(xm, state)
			return fmt.Errorf("external runtime applied but its revision was not persisted: %w", err)
		}
		state.State, state.Revision = "applied", revision
		setRuntimeStatus(xm, state)
		return nil
	}
	revision, restored, err := transitionRuntime(ctx, targets, func() error {
		if wantXray && !xm.Status() || wantSingbox && !sm.Status() {
			return errors.New("expected proxy engine stopped")
		}
		for _, in := range enabled {
			if err := ctx.Err(); err != nil {
				return err
			}
			if mode == "xray" && !IsXraySupported(in.Protocol) {
				continue
			}
			if result := diagnostic.ProbeInbound(in); !result.OK {
				return fmt.Errorf("inbound %q failed at %s: %s", in.Tag, result.Stage, result.Err)
			}
		}
		return nil
	})
	if err != nil {
		state.State, state.Error, state.RolledBack = "failed", err.Error(), restored
		if restored {
			state.State, state.Mode = "rolled_back", previous.Mode
		}
		if revision != "" && !restored {
			state.State = "rollback_failed"
		}
		setRuntimeStatus(xm, state)
		return err
	}
	xm.SetDualMode(mode == "auto")
	sm.SetDualMode(mode == "auto")
	state.State, state.Revision = "applied", revision
	setRuntimeStatus(xm, state)
	return persistRuntimeMode(mode)
}
