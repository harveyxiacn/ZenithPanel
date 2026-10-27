package updater

import (
	"path/filepath"
	"testing"
)

func TestStatusRoundTripAndIdle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-status.json")
	t.Setenv("ZENITH_UPDATE_STATUS_FILE", path)
	status, err := ReadStatus()
	if err != nil || status.State != "idle" {
		t.Fatalf("initial status=%+v err=%v", status, err)
	}
	want := Status{State: "running", Phase: "pull", Candidate: "repo@sha256:abc"}
	if err := writeStatus(want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if got.State != want.State || got.Phase != want.Phase || got.Candidate != want.Candidate || got.UpdatedAt == "" {
		t.Fatalf("status=%+v, want %+v with timestamp", got, want)
	}
}
