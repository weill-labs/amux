package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertRemotePaneViaSSH(t *testing.T, h *ServerHarness) {
	t.Helper()

	// Verify remote pane exists with correct metadata.
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(c.Panes))
	}
	p2 := h.jsonPane(c, "pane-2")
	if p2.Host != "test-remote" {
		t.Fatalf("pane-2 host = %q, want %q", p2.Host, "test-remote")
	}

	// Verify remote pane is functional (shell is running on localhost via SSH).
	h.sendKeys("pane-2", "echo REMOTE_SHELL_OK", "Enter")
	out := h.runCmd("wait", "content", "pane-2", "REMOTE_SHELL_OK", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should accept input: %s", out)
	}

	// Remote shells should also see TERM=amux and a resolvable amux terminfo entry.
	h.sendKeys("pane-2", "echo TERM=$TERM; infocmp amux >/dev/null && echo REMOTE_TERMINFO_OK", "Enter")
	out = h.runCmd("wait", "content", "pane-2", "TERM=amux", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should run with TERM=amux: %s", out)
	}
	out = h.runCmd("wait", "content", "pane-2", "REMOTE_TERMINFO_OK", "--timeout", "5s")
	if strings.Contains(out, "timeout") {
		t.Fatalf("remote pane should resolve amux terminfo: %s", out)
	}

	// Verify host metadata is visible in list command.
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "test-remote") {
		t.Errorf("list should show test-remote, got:\n%s", listOut)
	}
}

// TestRemotePaneViaSSH verifies that a remote proxy pane can be created
// over a real SSH connection (in-process test server) and is functional.
func TestRemotePaneViaSSH(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	splitRemotePane(t, h)
	assertRemotePaneViaSSH(t, h)
}

// TestRemotePaneViaSSHAutoDeploy verifies that the real remote connect path
// auto-deploys amux when the remote host starts without AMUX_BIN and without
// any usable amux in PATH.
func TestRemotePaneViaSSHAutoDeploy(t *testing.T) {
	t.Parallel()

	fixture := setupTestSSHNoPreload(t)
	remoteBin := filepath.Join(fixture.HomeDir, ".local", "bin", "amux")
	if _, err := os.Stat(remoteBin); err == nil {
		t.Fatalf("remote amux unexpectedly present before connect: %s", remoteBin)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat remote amux before connect: %v", err)
	}

	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(fixture.Addr, fixture.KeyFile))
	splitRemotePane(t, h)

	info, err := os.Stat(remoteBin)
	if err != nil {
		t.Fatalf("expected deployed remote amux at %s: %v", remoteBin, err)
	}
	if info.Mode()&0111 == 0 {
		t.Fatalf("deployed remote amux should be executable, mode=%v", info.Mode())
	}

	assertRemotePaneViaSSH(t, h)
}
