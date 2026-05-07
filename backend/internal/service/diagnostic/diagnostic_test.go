package diagnostic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveScriptPathPrefersPackagedScriptNextToExecutable(t *testing.T) {
	base := t.TempDir()
	execDir := filepath.Join(base, "bin")
	scriptPath := filepath.Join(execDir, "scripts", "vps_check.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	got, err := resolveScriptPath(filepath.Join(execDir, "zenithpanel"), filepath.Join(base, "unused"))
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if got != scriptPath {
		t.Fatalf("expected %q, got %q", scriptPath, got)
	}
}

func TestResolveScriptPathFallsBackToRepositoryScripts(t *testing.T) {
	base := t.TempDir()
	workDir := filepath.Join(base, "backend")
	scriptPath := filepath.Join(base, "scripts", "vps_check.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	got, err := resolveScriptPath(filepath.Join(workDir, "zenithpanel"), workDir)
	if err != nil {
		t.Fatalf("resolve script path: %v", err)
	}
	if got != scriptPath {
		t.Fatalf("expected %q, got %q", scriptPath, got)
	}
}

func TestResolveScriptPathReturnsErrorWhenNotFound(t *testing.T) {
	base := t.TempDir()
	_, err := resolveScriptPath(filepath.Join(base, "zenithpanel"), base)
	if err == nil {
		t.Fatalf("expected error when no script found")
	}
}
