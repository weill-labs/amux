package test

import (
	"fmt"
	"path/filepath"
	"testing"
)

// TestSplitInheritsCwd verifies that splitting a pane inherits the active
// pane's current working directory. The new shell should start in the same
// directory as the parent pane, matching tmux behavior.
func TestSplitInheritsCwd(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmpDir := t.TempDir()
	wantCwd := tmpDir
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil && resolved != "" {
		wantCwd = resolved
	}

	// Wait for canonical pwd output so we don't race on the echoed command line.
	h.sendKeys("pane-1", fmt.Sprintf("cd %q && pwd -P", tmpDir), "Enter")
	h.waitFor("pane-1", wantCwd)

	// Split — the new pane should inherit the source pane's cwd.
	h.splitV()

	h.sendKeys("pane-2", "pwd -P", "Enter")
	h.waitFor("pane-2", wantCwd)
}

// TestNewWindowInheritsCwd verifies that creating a new window inherits the
// active pane's current working directory.
func TestNewWindowInheritsCwd(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmpDir := t.TempDir()
	wantCwd := tmpDir
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil && resolved != "" {
		wantCwd = resolved
	}

	// Wait for canonical pwd output so we don't race on the echoed command line.
	h.sendKeys("pane-1", fmt.Sprintf("cd %q && pwd -P", tmpDir), "Enter")
	h.waitFor("pane-1", wantCwd)

	// Create a new window — its pane should inherit the source pane's cwd.
	gen := h.generation()
	h.runCmd("new-window")
	h.waitLayout(gen)

	h.sendKeys("pane-2", "pwd -P", "Enter")
	h.waitFor("pane-2", wantCwd)
}
