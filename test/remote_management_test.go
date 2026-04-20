package test

import (
	"strings"
	"testing"
)

// remoteHarness bundles a ServerHarness with SSH test infrastructure.
// Tests that need a remote host call splitRemotePane to create the connection.
func newRemoteHarness(t *testing.T) *ServerHarness {
	t.Helper()
	addr, keyFile := setupTestSSH(t)
	return newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))
}

func newPersistentRemoteHarness(t *testing.T) *ServerHarness {
	t.Helper()
	addr, keyFile := setupTestSSH(t)
	return newServerHarnessWithOptions(t, 80, 24, remoteTestConfig(addr, keyFile), false, false)
}

// splitRemotePane creates a remote pane on "test-remote" and waits for the
// layout to update. Fails the test if the split command errors.
func splitRemotePane(t *testing.T, h *ServerHarness) {
	t.Helper()
	gen := h.generation()
	out := h.runCmd("split", "pane-1", "--host", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote split failed: %s", out)
	}
	h.waitLayout(gen)
}

func connectRemoteSessionViaRemoteCLI(t *testing.T, h *ServerHarness) {
	t.Helper()
	gen := h.generation()
	out := h.runCmd("remote", "connect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("remote connect failed: %s", out)
	}
	h.waitLayout(gen)
}

// hostsShowsState checks that the `hosts` output contains the exact state
// string (e.g. "connected") without false-matching substrings like
// "disconnected" containing "connected".
func hostsShowsState(hostsOutput, state string) bool {
	for _, line := range strings.Split(hostsOutput, "\n") {
		if strings.Contains(line, "test-remote") {
			// The hosts table uses "%-15s" for state, so the state appears
			// as a whitespace-delimited field after the host name.
			fields := strings.Fields(line)
			for _, f := range fields {
				if f == state {
					return true
				}
			}
		}
	}
	return false
}

// TestHostsCommand verifies the `hosts` CLI command shows remote host status.
func TestHostsCommand(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)

	// Before connecting, hosts should show test-remote as disconnected
	out := h.runCmd("hosts")
	if !strings.Contains(out, "test-remote") {
		t.Fatalf("hosts should list test-remote, got:\n%s", out)
	}
	if !hostsShowsState(out, "disconnected") {
		t.Fatalf("hosts should show disconnected before any pane, got:\n%s", out)
	}

	// Split a remote pane to trigger connection
	splitRemotePane(t, h)
	c := h.captureJSON()
	assertCaptureConsistent(t, c)
	p2 := h.jsonPane(c, "pane-2")
	if p2.ConnStatus != "connected" {
		t.Fatalf("pane-2 conn_status = %q, want connected", p2.ConnStatus)
	}

	// After connecting, hosts should show connected (not "disconnected")
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "connected") {
		t.Fatalf("hosts should show connected after split, got:\n%s", out)
	}
}

// TestDisconnectAndReconnect verifies the disconnect and reconnect CLI commands.
func TestDisconnectAndReconnect(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)
	splitRemotePane(t, h)

	// Verify pane is functional
	h.sendKeys("pane-2", "echo REMOTE_OK", "Enter")
	h.waitForTimeout("pane-2", "REMOTE_OK", "5s")

	// Disconnect
	gen := h.generation()
	out := h.runCmd("disconnect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("disconnect failed: %s", out)
	}
	if !strings.Contains(out, "Disconnected") {
		t.Errorf("disconnect should confirm, got: %s", out)
	}
	h.waitLayout(gen)
	c := h.captureJSON()
	assertCaptureConsistent(t, c)
	p2 := h.jsonPane(c, "pane-2")
	if p2.ConnStatus != "disconnected" {
		t.Fatalf("pane-2 conn_status after disconnect = %q, want disconnected", p2.ConnStatus)
	}

	// Verify hosts shows disconnected
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "disconnected") {
		t.Errorf("hosts should show disconnected after disconnect, got:\n%s", out)
	}

	// Reconnect
	gen = h.generation()
	out = h.runCmd("reconnect", "test-remote")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("reconnect failed: %s", out)
	}
	if !strings.Contains(out, "Reconnected") {
		t.Errorf("reconnect should confirm, got: %s", out)
	}
	h.waitLayout(gen)
	c = h.captureJSON()
	assertCaptureConsistent(t, c)
	p2 = h.jsonPane(c, "pane-2")
	if p2.ConnStatus != "connected" {
		t.Fatalf("pane-2 conn_status after reconnect = %q, want connected", p2.ConnStatus)
	}

	h.sendKeys("pane-2", "echo RECONNECTED_OK", "Enter")
	h.waitForTimeout("pane-2", "RECONNECTED_OK", "5s")

	// Verify hosts shows connected again
	out = h.runCmd("hosts")
	if !hostsShowsState(out, "connected") {
		t.Errorf("hosts should show connected after reconnect, got:\n%s", out)
	}
}

func TestConnectCaptureAndDisconnect(t *testing.T) {
	t.Parallel()

	h := newPersistentRemoteHarness(t)
	connectRemoteSessionViaRemoteCLI(t, h)

	c := h.captureJSON()
	assertCaptureConsistent(t, c)
	if len(c.Panes) == 0 {
		t.Fatal("connect should leave at least one visible pane in capture")
	}

	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "HOST") {
		t.Fatalf("list output missing HOST column:\n%s", listOut)
	}
	if !strings.Contains(listOut, "test-remote") {
		t.Fatalf("list output missing remote host entry:\n%s", listOut)
	}
	if !strings.Contains(listOut, "pane-1") {
		t.Fatalf("list output missing mirrored remote pane name:\n%s", listOut)
	}

	gen := h.generation()
	out := h.runCmd("remote", "disconnect", "test-remote")
	if !strings.Contains(out, "Disconnected from test-remote") {
		t.Fatalf("disconnect should confirm, got: %s", out)
	}
	h.waitLayout(gen)

	c = h.captureJSON()
	assertCaptureConsistent(t, c)
	listOut = h.runCmd("list")
	for _, line := range strings.Split(listOut, "\n") {
		if strings.Contains(line, "test-remote") {
			t.Fatalf("disconnect should remove remote panes from list, still found:\n%s", listOut)
		}
	}

	out = h.runCmd("remote", "hosts")
	if !hostsShowsState(out, "disconnected") {
		t.Fatalf("hosts should show disconnected after connect/disconnect, got:\n%s", out)
	}
}

// TestRemotePaneKill verifies that killing a remote pane cleans up mappings.
func TestRemotePaneKill(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)
	splitRemotePane(t, h)

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	assertCaptureConsistent(t, c)
	p2 := h.jsonPane(c, "pane-2")
	if p2.ConnStatus != "connected" {
		t.Fatalf("pane-2 conn_status before kill = %q, want connected", p2.ConnStatus)
	}

	// Kill the remote pane
	gen := h.generation()
	out := h.runCmd("kill", "pane-2")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("kill pane-2 failed: %s", out)
	}
	h.waitLayout(gen)

	c = h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("expected 1 pane after kill, got %d", len(c.Panes))
	}
	assertCaptureConsistent(t, c)
}

func TestRemotePaneKillCleanup(t *testing.T) {
	t.Parallel()

	h := newRemoteHarness(t)
	splitRemotePane(t, h)

	h.sendKeys("pane-2", "echo REMOTE_CLEANUP_READY; trap 'sleep 0.3; exit 0' TERM; while :; do sleep 1; done", "Enter")
	h.waitForTimeout("pane-2", "REMOTE_CLEANUP_READY", "5s")

	gen := h.generation()
	out := h.runCmd("kill", "--cleanup", "--timeout", "100ms", "pane-2")
	if strings.Contains(out, "error") || strings.Contains(out, "Error") {
		t.Fatalf("kill --cleanup pane-2 failed: %s", out)
	}
	if !strings.Contains(out, "Cleaning up pane-2") {
		t.Fatalf("kill --cleanup should confirm, got: %s", out)
	}

	h.waitLayoutTimeout(gen, "10s")
	c := h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("expected 1 pane after cleanup kill, got %d", len(c.Panes))
	}
	assertCaptureConsistent(t, c)
}
