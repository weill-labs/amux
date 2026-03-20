package test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
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
	existingProxyPanes := takeoverProxyPaneNames(h)

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

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)

	// Verify bidirectional I/O: the proxy pane must accept keystrokes and show
	// their output. This is the core regression: proxy panes were created but
	// non-interactive (input went to SSH stdin → ignored, output never routed back).
	h.sendKeys(proxyPaneName, "echo TAKEOVER_IO_OK", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_IO_OK", "5s")
}

func TestTakeoverFromInteractiveSSHShell(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -tt -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")
	h.sendKeys("pane-1", "echo SSH_SHELL_READY", "Enter")
	h.waitForTimeout("pane-1", "SSH_SHELL_READY", "5s")

	existingProxyPanes := takeoverProxyPaneNames(h)
	h.sendKeys("pane-1", "amux", "Enter")

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)
	h.sendKeys(proxyPaneName, "echo TAKEOVER_SHELL_OK", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_SHELL_OK", "5s")
}

func TestTakeoverAttachFailureLeavesSSHPaneVisible(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	h.sendKeys("pane-1",
		`printf '\033]999;amux-takeover;{"session":"default@badhost","host":"badhost","uid":"1","ssh_address":"127.0.0.1:1","ssh_user":"nobody","panes":[{"id":1,"name":"pane-1","cols":80,"rows":22}]}\007'`,
		"Enter",
	)
	h.waitIdle("pane-1")

	list := h.runCmd("list")
	if strings.Contains(list, "@badhost") {
		t.Fatalf("failed takeover should not splice proxy panes\nlist:\n%s", list)
	}

	c := h.captureJSON()
	if len(c.Panes) != 1 || c.Panes[0].Name != "pane-1" {
		t.Fatalf("failed takeover should leave only the raw SSH pane\ncapture:\n%s", h.capture())
	}
}

func TestTakeoverReconnectAfterRemoteReload(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))
	existingProxyPanes := takeoverProxyPaneNames(h)

	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)
	h.sendKeys(proxyPaneName, "echo TAKEOVER_BEFORE_RELOAD", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_BEFORE_RELOAD", "5s")

	gen := h.generation()
	h.sendKeys(proxyPaneName, "amux reload-server", "Enter")
	h.waitLayoutTimeout(gen, "10s")
	waitForPaneConnStatus(t, h, proxyPaneName, "connected", "10s")

	h.sendKeys(proxyPaneName, "echo TAKEOVER_AFTER_RELOAD", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_AFTER_RELOAD", "10s")
}

// TestTakeoverAfterServerReload is a regression test for the bug where
// NewServerFromCheckpoint didn't call SetOnTakeover on restored panes.
// Without the fix, an SSH takeover emitted after a reload is silently ignored
// instead of invoking handleTakeover.
func TestTakeoverAfterServerReload(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))

	h.runCmd("reload-server")
	_ = h.generation()

	existingProxyPanes := map[string]struct{}{}
	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)
	h.sendKeys(proxyPaneName, "echo TAKEOVER_AFTER_RELOAD_OK", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_AFTER_RELOAD_OK", "5s")
}

func waitForTakeoverProxyPane(t *testing.T, h *ServerHarness, existing map[string]struct{}) string {
	t.Helper()

	gen := h.generation()
	for h.waitLayoutOrTimeout(gen, "5s") {
		if proxyPaneName := firstNewTakeoverProxyPane(h, existing); proxyPaneName != "" {
			return proxyPaneName
		}
		gen = h.generation()
	}

	logPath := fmt.Sprintf("%s/%s.log", server.SocketDir(), h.session)
	logData, _ := os.ReadFile(logPath)
	t.Fatalf("takeover proxy pane did not appear\nlist:\n%s\npane-1:\n%s",
		h.runCmd("list"), h.runCmd("capture", "pane-1")+"\nserver log:\n"+string(logData))
	return ""
}

func firstNewTakeoverProxyPane(h *ServerHarness, existing map[string]struct{}) string {
	capture, ok := takeoverCaptureJSON(h)
	if ok {
		for _, p := range capture.Panes {
			if !strings.Contains(p.Name, "@") {
				continue
			}
			if _, ok := existing[p.Name]; ok {
				continue
			}
			return p.Name
		}
	}

	for _, line := range strings.Split(h.runCmd("list"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if !strings.Contains(name, "@") {
			continue
		}
		if _, ok := existing[name]; ok {
			continue
		}
		return name
	}
	return ""
}

func takeoverProxyPaneNames(h *ServerHarness) map[string]struct{} {
	names := make(map[string]struct{})
	capture, ok := takeoverCaptureJSON(h)
	if !ok {
		return names
	}
	for _, p := range capture.Panes {
		if strings.Contains(p.Name, "@") {
			names[p.Name] = struct{}{}
		}
	}
	return names
}

func waitForPaneConnStatus(t *testing.T, h *ServerHarness, paneName, wantStatus, timeout string) {
	t.Helper()

	deadline := time.Now().Add(parseTestDuration(t, timeout))
	gen := h.generation()
	for time.Now().Before(deadline) {
		if c, ok := takeoverCaptureJSON(h); ok {
			for _, p := range c.Panes {
				if p.Name == paneName && p.ConnStatus == wantStatus {
					return
				}
			}
		}

		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !h.waitLayoutOrTimeout(gen, waitFor.String()) {
			continue
		}
		gen = h.generation()
	}

	t.Fatalf("pane %s did not reach conn_status=%s\ncapture:\n%s", paneName, wantStatus, h.capture())
}

func takeoverCaptureJSON(h *ServerHarness) (proto.CaptureJSON, bool) {
	h.tb.Helper()

	out := h.runCmd("capture", "--format", "json")
	if strings.Contains(out, "no client attached") {
		return proto.CaptureJSON{}, false
	}

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		h.tb.Fatalf("captureJSON: %v\nraw: %s", err, out)
	}
	return capture, true
}

func parseTestDuration(t *testing.T, s string) time.Duration {
	t.Helper()

	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("ParseDuration(%q): %v", s, err)
	}
	return d
}
