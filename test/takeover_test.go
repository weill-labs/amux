package test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
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
		"ssh -t -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
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

	c := waitForSessionNotice(t, h, "takeover badhost", "5s")
	if len(c.Panes) != 1 || c.Panes[0].Name != "pane-1" {
		t.Fatalf("failed takeover should leave only the raw SSH pane\ncapture:\n%s", h.capture())
	}
	const wantNoticePrefix = "takeover badhost (127.0.0.1:1): "
	if !strings.HasPrefix(c.Notice, wantNoticePrefix) || len(c.Notice) == len(wantNoticePrefix) {
		t.Fatalf("expected takeover failure notice in JSON capture, got %+v", c)
	}
	if screen := h.capture(); !strings.Contains(screen, "takeover badhost") {
		t.Fatalf("expected takeover failure notice in rendered capture\ncapture:\n%s", screen)
	}
}

func TestTakeoverAttachHostKeyMismatchShowsNotice(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))
	writeMismatchedKnownHost(t, h.home, addr)

	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -t -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	c := waitForSessionNotice(t, h, "SSH host key verification failed", "5s")
	if strings.Contains(h.runCmd("list"), "@") {
		t.Fatalf("host-key mismatch should not splice proxy panes\nlist:\n%s", h.runCmd("list"))
	}
	if !strings.Contains(c.Notice, addr) {
		t.Fatalf("notice should include target address %s, got %q", addr, c.Notice)
	}
	if !strings.Contains(c.Notice, "SSH host key verification failed") {
		t.Fatalf("notice should include host-key failure, got %q", c.Notice)
	}
}

func TestTakeoverFailureNoticeExpires(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithOptions(t, 80, 24, "", true, "AMUX_NOTICE_DURATION=500ms")
	h.sendKeys("pane-1",
		`printf '\033]999;amux-takeover;{"session":"default@badhost","host":"badhost","uid":"1","ssh_address":"127.0.0.1:1","ssh_user":"nobody","panes":[{"id":1,"name":"pane-1","cols":80,"rows":22}]}\007'`,
		"Enter",
	)

	waitForSessionNotice(t, h, "takeover badhost", "5s")
	waitForSessionNoticeGone(t, h, "5s")
	if c := h.captureJSON(); c.Notice != "" {
		t.Fatalf("expected session notice to expire, got %+v", c)
	}
}

func TestTakeoverReconnectAfterRemoteReload(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithOptions(t, 80, 24, remoteTestConfig(addr, keyFile), false)
	existingProxyPanes := takeoverProxyPaneNames(h)

	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -t -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)
	waitForPaneConnStatus(t, h, proxyPaneName, "connected", "10s")
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
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]")
	}, 15*time.Second) {
		t.Fatalf("session did not recover after reload-server\ncapture:\n%s", h.capture())
	}

	existingProxyPanes := map[string]struct{}{}
	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -t -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")

	proxyPaneName := waitForTakeoverProxyPane(t, h, existingProxyPanes)
	h.sendKeys(proxyPaneName, "echo TAKEOVER_AFTER_RELOAD_OK", "Enter")
	h.waitForTimeout(proxyPaneName, "TAKEOVER_AFTER_RELOAD_OK", "5s")
}

// TestTakeoverSkippedWhenTermNotAmux verifies that the takeover gate correctly
// skips takeover when TERM is not "amux" (e.g., SSH from a phone or plain terminal).
// SSH without -t means no pty-req, so TERM stays as xterm-256color on the remote.
func TestTakeoverSkippedWhenTermNotAmux(t *testing.T) {
	t.Parallel()

	addr, keyFile := setupTestSSH(t)
	h := newServerHarnessWithConfig(t, 80, 24, remoteTestConfig(addr, keyFile))
	existingProxyPanes := takeoverProxyPaneNames(h)

	// SSH without -t: no pty-req sent, so TERM defaults to xterm-256color.
	// The remote amux should NOT attempt takeover (TERM != "amux").
	// Without a PTY the remote amux will fail to attach, but the point is
	// that tryTakeover is never called and no proxy panes appear.
	_, port, _ := net.SplitHostPort(addr)
	sshCmd := fmt.Sprintf(
		"ssh -i %s -p %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null 127.0.0.1 amux 2>&1; echo NO_TAKEOVER_DONE",
		keyFile, port)
	h.sendKeys("pane-1", sshCmd, "Enter")
	h.waitForTimeout("pane-1", "NO_TAKEOVER_DONE", "5s")

	if name := firstNewTakeoverProxyPane(h, existingProxyPanes); name != "" {
		t.Fatalf("takeover should not trigger without TERM=amux, but got proxy pane %s", name)
	}
}

func waitForTakeoverProxyPane(t *testing.T, h *ServerHarness, existing map[string]struct{}) string {
	t.Helper()

	gen := takeoverGeneration(t, h)
	for takeoverWaitLayoutOrTimeout(h, gen, "5s") {
		if proxyPaneName := firstNewTakeoverProxyPane(h, existing); proxyPaneName != "" {
			return proxyPaneName
		}
		gen = takeoverGeneration(t, h)
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
	gen := takeoverGeneration(t, h)
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
		if !takeoverWaitLayoutOrTimeout(h, gen, waitFor.String()) {
			continue
		}
		gen = takeoverGeneration(t, h)
	}

	t.Fatalf("pane %s did not reach conn_status=%s\ncapture:\n%s", paneName, wantStatus, h.capture())
}

func waitForSessionNotice(t *testing.T, h *ServerHarness, substr, timeout string) proto.CaptureJSON {
	t.Helper()

	deadline := time.Now().Add(parseTestDuration(t, timeout))
	gen := takeoverGeneration(t, h)
	for time.Now().Before(deadline) {
		capture := h.captureJSON()
		if strings.Contains(capture.Notice, substr) {
			return capture
		}

		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !takeoverWaitLayoutOrTimeout(h, gen, waitFor.String()) {
			continue
		}
		gen = takeoverGeneration(t, h)
	}

	t.Fatalf("session notice %q did not appear\ncapture:\n%s", substr, h.capture())
	return proto.CaptureJSON{}
}

func waitForSessionNoticeGone(t *testing.T, h *ServerHarness, timeout string) {
	t.Helper()

	deadline := time.Now().Add(parseTestDuration(t, timeout))
	gen := takeoverGeneration(t, h)
	for time.Now().Before(deadline) {
		if h.captureJSON().Notice == "" {
			return
		}

		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if !takeoverWaitLayoutOrTimeout(h, gen, waitFor.String()) {
			continue
		}
		gen = takeoverGeneration(t, h)
	}

	t.Fatalf("session notice did not clear\ncapture:\n%s", h.capture())
}

func takeoverCaptureJSON(h *ServerHarness) (proto.CaptureJSON, bool) {
	h.tb.Helper()

	out := h.runCmd("capture", "--format", "json")
	if strings.Contains(out, "no client attached") ||
		strings.Contains(out, "amux capture: EOF") ||
		strings.Contains(out, "amux capture: server not running") {
		return proto.CaptureJSON{}, false
	}

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		h.tb.Fatalf("captureJSON: %v\nraw: %s", err, out)
	}
	return capture, true
}

func takeoverGeneration(t *testing.T, h *ServerHarness) uint64 {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var out string
	for {
		out = strings.TrimSpace(h.runCmd("generation"))
		n, err := strconv.ParseUint(out, 10, 64)
		if err == nil {
			return n
		}
		if !strings.Contains(out, "server not running") || time.Now().After(deadline) {
			logPath := fmt.Sprintf("%s/%s.log", server.SocketDir(), h.session)
			logData, _ := os.ReadFile(logPath)
			t.Fatalf("parsing generation: %v (output: %q)\nserver log:\n%s", err, out, string(logData))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func takeoverWaitLayoutOrTimeout(h *ServerHarness, afterGen uint64, timeout string) bool {
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(afterGen, 10), "--timeout", timeout)
	if strings.Contains(out, "server not running") {
		return false
	}
	return !strings.Contains(out, "timeout")
}

func parseTestDuration(t *testing.T, s string) time.Duration {
	t.Helper()

	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("ParseDuration(%q): %v", s, err)
	}
	return d
}

func writeMismatchedKnownHost(t *testing.T, home, addr string) {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating mismatched host key: %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("creating ssh dir: %v", err)
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(addr)}, key)
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("writing known_hosts: %v", err)
	}
}
