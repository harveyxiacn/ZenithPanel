package diagnostic

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ErrDiagnosticScriptUnavailable is returned when no vps_check.sh script can
// be located in any of the known candidate paths. Callers should surface this
// as a 503 rather than a generic 500 so operators understand the issue.
var ErrDiagnosticScriptUnavailable = errors.New("diagnostic script unavailable")

// resolveScriptPath searches for vps_check.sh relative to executablePath and
// workingDir. Packaged deployments store the script next to the binary;
// source/dev layouts store it at the repository root.
func resolveScriptPath(executablePath, workingDir string) (string, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(executablePath), "scripts", "vps_check.sh"),
		filepath.Join(filepath.Dir(executablePath), "..", "scripts", "vps_check.sh"),
		filepath.Join(workingDir, "scripts", "vps_check.sh"),
		filepath.Join(workingDir, "..", "scripts", "vps_check.sh"),
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", ErrDiagnosticScriptUnavailable
}

// RunNetworkDiagnostic executes the vps_check.sh script and returns its output.
// Returns ErrDiagnosticScriptUnavailable if the script cannot be found.
func RunNetworkDiagnostic() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	scriptPath, err := resolveScriptPath(execPath, workingDir)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err = cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out.String(), context.DeadlineExceeded
		}
		return out.String(), err
	}

	return out.String(), nil
}
