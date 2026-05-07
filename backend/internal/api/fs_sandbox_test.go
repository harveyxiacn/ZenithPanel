package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathWithinSandboxRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	sibling := root + "2"
	if pathWithinSandbox(root, sibling) {
		t.Fatalf("expected sibling path %q to be rejected for root %q", sibling, root)
	}
}

func TestPathWithinSandboxAcceptsRootItself(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if !pathWithinSandbox(root, root) {
		t.Fatalf("expected root itself to be accepted")
	}
}

func TestPathWithinSandboxAcceptsDescendant(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	child := filepath.Join(root, "user", "docs")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	if !pathWithinSandbox(root, child) {
		t.Fatalf("expected descendant %q to be accepted under %q", child, root)
	}
}

func TestPathWithinSandboxRejectsParentTraversal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	outside := filepath.Dir(root)
	if pathWithinSandbox(root, outside) {
		t.Fatalf("expected parent path %q to be rejected for root %q", outside, root)
	}
}

func TestResolveSandboxPathRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported on this runner: %v", err)
	}

	if _, ok := resolveSandboxPath(root, filepath.Join(link, "secret.txt")); ok {
		t.Fatalf("expected symlink escape to be rejected")
	}
}

func TestResolveSandboxPathAcceptsValidPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	child := filepath.Join(root, "user", "file.txt")
	if err := os.MkdirAll(filepath.Dir(child), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(child, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	resolved, ok := resolveSandboxPath(root, child)
	if !ok {
		t.Fatalf("expected valid path %q to be accepted under %q", child, root)
	}
	if resolved == "" {
		t.Fatalf("expected non-empty resolved path")
	}
}
