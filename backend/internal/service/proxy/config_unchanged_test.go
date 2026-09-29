package proxy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	if err := os.WriteFile(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	same := func() (string, error) { return `{"a":1}`, nil }
	diff := func() (string, error) { return `{"a":2}`, nil }
	fail := func() (string, error) { return "", errors.New("boom") }

	if !configUnchanged(true, path, same) {
		t.Error("running + identical config should be unchanged")
	}
	if configUnchanged(false, path, same) {
		t.Error("stopped engine must be (re)started")
	}
	if configUnchanged(true, path, diff) {
		t.Error("different config must restart")
	}
	if configUnchanged(true, path, fail) {
		t.Error("generation error must fall back to restart")
	}
	if configUnchanged(true, filepath.Join(t.TempDir(), "missing.json"), same) {
		t.Error("missing on-disk config must restart")
	}
}
