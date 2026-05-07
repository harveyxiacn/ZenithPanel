package api

import (
	"os"
	"path/filepath"
	"strings"
)

// resolveSandboxPath resolves userPath and verifies it is contained within root.
// It resolves symlinks on both root and the candidate to prevent escape via symlink chains.
// For paths that do not yet exist (e.g. new file writes), the parent is resolved instead.
// Returns the cleaned absolute path and true on success; "", false if the path escapes the sandbox.
func resolveSandboxPath(root, userPath string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = absRoot
	}

	candidate, err := filepath.Abs(filepath.Clean(userPath))
	if err != nil {
		return "", false
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// Path may not exist yet (write operation): resolve parent instead.
		parent := filepath.Dir(candidate)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr != nil {
			return "", false
		}
		resolvedCandidate = filepath.Join(resolvedParent, filepath.Base(candidate))
	}

	if !pathWithinSandbox(resolvedRoot, resolvedCandidate) {
		return "", false
	}
	// Reject dangling symlinks at the final component to prevent TOCTOU escape.
	if info, err := os.Lstat(candidate); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}

	return candidate, true
}

// pathWithinSandbox reports whether candidate is root itself or a descendant of root.
// Uses filepath.Rel to avoid the sibling-prefix bug (e.g. /home2 matching /home).
func pathWithinSandbox(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// isPathSafe is the package-level entry point used by file-manager route handlers.
// It delegates to resolveSandboxPath using the configured fsSandboxRoot.
func isPathSafe(userPath string) (string, bool) {
	return resolveSandboxPath(fsSandboxRoot, userPath)
}
