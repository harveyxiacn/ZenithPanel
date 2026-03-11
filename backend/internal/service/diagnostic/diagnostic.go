package diagnostic

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

// RunNetworkDiagnostic executes the vps_check.sh script and captures its output
func RunNetworkDiagnostic() (string, error) {
	// Typically the script is at /scripts/vps_check.sh relative to the project root
	// In production, the binary path resolution is more robust
	scriptPath, _ := filepath.Abs("../scripts/vps_check.sh")

	// Set a 3-minute timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return out.String(), context.DeadlineExceeded
		}
		// Return the buffer even on error as the script might return non-0 exit status occasionally
		return out.String(), err
	}

	return out.String(), nil
}
