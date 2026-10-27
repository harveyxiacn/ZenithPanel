package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const defaultStatusPath = "/opt/zenithpanel/data/update-status.json"

// Status is persisted on the shared data volume so the replacement process
// can report whether the helper committed the candidate or rolled it back.
type Status struct {
	State      string `json:"state"`
	Phase      string `json:"phase,omitempty"`
	Message    string `json:"message,omitempty"`
	Previous   string `json:"previous_image,omitempty"`
	Candidate  string `json:"candidate_image,omitempty"`
	RolledBack bool   `json:"rolled_back,omitempty"`
	UpdatedAt  string `json:"updated_at"`
}

var statusMu sync.Mutex

func statusPath() string {
	if configured := strings.TrimSpace(os.Getenv("ZENITH_UPDATE_STATUS_FILE")); configured != "" {
		return configured
	}
	return defaultStatusPath
}

func writeStatus(status Status) error {
	statusMu.Lock()
	defer statusMu.Unlock()
	if status.UpdatedAt == "" {
		status.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	payload, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	path := statusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create update status directory: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write update status: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit update status: %w", err)
	}
	return nil
}

// ReadStatus returns the latest durable OTA state. A host that has never run
// an OTA reports idle instead of treating the absent file as an error.
func ReadStatus() (Status, error) {
	statusMu.Lock()
	defer statusMu.Unlock()
	payload, err := os.ReadFile(statusPath())
	if errors.Is(err, os.ErrNotExist) {
		return Status{State: "idle"}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("read update status: %w", err)
	}
	var status Status
	if err := json.Unmarshal(payload, &status); err != nil {
		return Status{}, fmt.Errorf("parse update status: %w", err)
	}
	if status.State == "" {
		return Status{}, fmt.Errorf("parse update status: missing state")
	}
	return status, nil
}
