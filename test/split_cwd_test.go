package test

import "testing"

// TestSplitInheritsCwd verifies that splitting a pane inherits the active
// pane's current working directory. The new shell should start in the same
// directory as the parent pane, matching tmux behavior.
func TestSplitInheritsCwd(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Change pane-1's working directory to /tmp.
	h.runShellCommand("pane-1", "cd /tmp && echo CWD_READY", "CWD_READY")

	// Split — the new pane should inherit /tmp as its cwd.
	h.splitV()

	// Ask pane-2 for its working directory. On macOS, /tmp resolves to
	// /private/tmp, so check for the common suffix.
	h.sendKeys("pane-2", "pwd", "Enter")
	h.waitFor("pane-2", "tmp")
}

// TestNewWindowInheritsCwd verifies that creating a new window inherits the
// active pane's current working directory.
func TestNewWindowInheritsCwd(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Change pane-1's working directory to /tmp.
	h.runShellCommand("pane-1", "cd /tmp && echo CWD_READY", "CWD_READY")

	// Create a new window — its pane should inherit /tmp.
	gen := h.generation()
	h.runCmd("new-window")
	h.waitLayout(gen)

	// The new window's pane (pane-2) should start in /tmp.
	h.sendKeys("pane-2", "pwd", "Enter")
	h.waitFor("pane-2", "tmp")
}
