package updater

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSwapScriptExecutesCommitAndRecovery(t *testing.T) {
	shell := "sh"
	if runtime.GOOS == "windows" {
		shell = `C:\Program Files\Git\bin\bash.exe`
	}
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	for _, scenario := range []struct {
		name, state string
		success     bool
	}{
		{"healthy", "succeeded", true}, {"unready", "rolled_back", false},
		{"start_failed", "rolled_back", false}, {"rollback_start_failed", "rollback_failed", false},
		{"rename_failed", "rollback_failed", false}, {"remove_old_failed", "rolled_back", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			statusPath := filepath.ToSlash(filepath.Join(dir, "status.json"))
			logPath := filepath.ToSlash(filepath.Join(dir, "docker.log"))
			prefix := `docker() {
 printf '%s\n' "$*" >> "$TEST_LOG"
 case "$1" in
  exec) if [ "$2" = "new" ] && [ "$SCENARIO" != "healthy" ] && [ "$SCENARIO" != "remove_old_failed" ]; then return 1; fi ;;
  start) if [ "$2" = "new" ] && [ "$SCENARIO" = "start_failed" ]; then return 1; fi
         if [ "$2" = "old" ] && [ "$SCENARIO" = "rollback_start_failed" ]; then return 1; fi ;;
  rename) if [ "$SCENARIO" = "rename_failed" ]; then return 1; fi ;;
  rm) if [ "$2" = "old" ] && [ "$SCENARIO" = "remove_old_failed" ]; then return 1; fi ;;
 esac
 return 0
}
sleep() { :; }
`
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shell, "-c", prefix+swapScript())
			cmd.Env = append(os.Environ(), "SCENARIO="+scenario.name, "TEST_LOG="+logPath, "ZENITH_UPDATE_STATUS_FILE="+statusPath, "ZENITH_OLD_ID=old", "ZENITH_NEW_ID=new", "ZENITH_ORIGINAL_NAME=panel", "ZENITH_PREVIOUS_IMAGE=previous", "ZENITH_CANDIDATE_IMAGE=candidate")
			output, err := cmd.CombinedOutput()
			if (err == nil) != scenario.success {
				t.Fatalf("exit: %v, %s", err, output)
			}
			data, err := os.ReadFile(statusPath)
			if err != nil {
				t.Fatalf("status: %v, %s", err, output)
			}
			var status Status
			if err := json.Unmarshal(data, &status); err != nil {
				t.Fatal(err)
			}
			if status.State != scenario.state || status.RolledBack != (scenario.state == "rolled_back") {
				t.Fatalf("status: %+v", status)
			}
			calls, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.success {
				if strings.Count(string(calls), "-q ready") != 3 {
					t.Fatalf("must require consecutive readiness: %s", calls)
				}
			} else if strings.Contains(string(calls), "image prune") {
				t.Fatal("pruned images after failed swap")
			}
		})
	}
}
