package test

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestTakeoverBidirectionalIO verifies the full SSH takeover I/O pipeline:
// proxy panes must be interactive after a takeover, not just visible in the layout.
//
// Before the fix, proxy panes appeared in the layout but were non-interactive:
//   - writeOverride routed to the SSH PTY stdin → remote amux server stdin → ignored
//   - No output path existed (nothing called proxyPane.FeedOutput)
//
// After the fix, handleTakeover connects back to the remote amux server via SSH
// (using req.SSHAddress from SSH_CONNECTION) and wires SendInput/FeedOutput.
func TestTakeoverBidirectionalIO(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	// SSH into the test server and run amux. The remote amux detects SSH_CONNECTION,
	// emits an OSC 999 takeover sequence to stdout (the SSH PTY), and waits for ack.
	// The local server's readLoop detects the sequence, calls handleTakeover, which
	// sends a TakeoverAck carrying the agreed session name, then connects back via SSH
	// to wire bidirectional I/O.
	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	// Wait for a takeover proxy pane to appear. Proxy pane names have the form
	// "<remote-pane-name>@<hostname>" (set by handleTakeover). The initial local
	// pane is named "pane-1" with no "@", so this correctly identifies only
	// takeover proxy panes.
	gen := h.generation()
	var proxyPaneName string
	for h.waitLayoutOrTimeout(gen, "5s") {
		c := h.captureJSON()
		for _, p := range c.Panes {
			if strings.Contains(p.Name, "@") {
				proxyPaneName = p.Name
				break
			}
		}
		if proxyPaneName != "" {
			break
		}
		gen = h.generation()
	}
	if proxyPaneName == "" {
		t.Fatalf("takeover proxy pane did not appear; list:\n%s", h.runCmd("list"))
	}

	// Verify bidirectional I/O: the proxy pane must accept keystrokes and show
	// their output. This is the core regression: proxy panes were created but
	// non-interactive (input went to SSH stdin → ignored, output never routed back).
	h.sendKeys(proxyPaneName, "echo TAKEOVER_IO_OK", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_IO_OK", "5s")
}

// TestTakeoverAfterServerReload is a regression test for the bug where
// NewServerFromCheckpoint didn't call SetOnTakeover on restored panes.
// Without the fix, pane.onTakeover == nil after a reload, so the readLoop
// silently ignores all SSH takeover sequences (OSC 999) instead of calling
// handleTakeover.
func TestTakeoverAfterServerReload(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Trigger a server hot-reload (checkpoint + syscall.Exec — same PID,
	// new process image). The headless client connection drops on exec;
	// the new server inherits the listener FD so connections are queued
	// in the OS backlog immediately.
	h.runCmd("reload-server")

	// Capture generation immediately after reload (new server starts at 0).
	// h.generation() also validates the server is accepting connections —
	// if it can't connect it calls t.Fatalf.
	gen := h.generation()

	// Have pane-1 emit an SSH takeover sequence. In production, the remote
	// amux binary prints this to its stdout (the SSH PTY); here the local
	// shell does the same thing. The server's readLoop detects the OSC 999
	// sequence and calls handleTakeover.
	const hostname = "testhost"
	h.sendKeys("pane-1",
		`printf '\033]999;amux-takeover;{"session":"s","host":"testhost","uid":"1","panes":[{"id":1,"name":"pane-1","cols":80,"rows":22}]}\007'`,
		"Enter")

	// The idle→busy pane-activity transition calls broadcastLayout, and
	// handleTakeover calls it again after splicing — so we may see multiple
	// layout bumps before the takeover completes. Loop until the proxy pane
	// appears or no more layout changes arrive.
	for h.waitLayoutOrTimeout(gen, "5s") {
		list := h.runCmd("list")
		if strings.Contains(list, hostname) {
			return // takeover fired correctly
		}
		gen = h.generation()
	}
	t.Errorf("takeover after reload: expected pane@%s in list output\n%s", hostname, h.runCmd("list"))
}
