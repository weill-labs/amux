//go:build !race

package server

import (
	"strings"
	"sync"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func setupWaitReadyTestPane(t *testing.T, writeOverride func([]byte) (int, error)) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	if writeOverride == nil {
		writeOverride = func(data []byte) (int, error) { return len(data), nil }
	}
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[0],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), writeOverride)
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}
	return srv, sess, pane, cleanup
}

func codexReadyScreen(placeholder string) string {
	return "\x1b[2J\x1b[H" + strings.Repeat("\r\n", 19) + "› " + placeholder
}

func codexTrustDialogScreen(path string) string {
	return "\x1b[2J\x1b[H> You are in " + path + "\r\n\r\n" +
		"  Do you trust the contents of this directory? Working with untrusted contents\r\n" +
		"  comes with higher risk of prompt injection.\r\n\r\n" +
		"› 1. Yes, continue\r\n" +
		"  2. No, quit\r\n\r\n" +
		"  Press enter to continue\x1b[?25l"
}

func claudePromptScreen() string {
	return "\x1b[2J\x1b[H" +
		strings.Repeat("\r\n", 6) +
		"❯ \x1b[7m \x1b[m" +
		"\x1b[?25l" +
		"\x1b[11;1H"
}

func numberedMenuScreen() string {
	return "\x1b[2J\x1b[H" + strings.Repeat("\r\n", 5) + "› 1. Yes, continue"
}

func TestWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "wait-ready")
	if got := res.cmdErr; got != "usage: wait-ready <pane> [--timeout <duration>] [--continue-known-dialogs]" {
		t.Fatalf("wait-ready usage error = %q", got)
	}
}

func TestWaitReadyMatchesCodexPrompt(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(codexReadyScreen("Implement {feature}")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready result = %#v", res)
	}
}

func TestWaitReadyBlocksOnCodexTrustDialog(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if !strings.Contains(res.cmdErr, "Codex trust dialog is blocking input in pane-1") {
		t.Fatalf("wait-ready dialog error = %#v", res)
	}
}

func TestWaitReadyCanContinueKnownCodexDialog(t *testing.T) {
	t.Parallel()

	var (
		pane   *mux.Pane
		mu     sync.Mutex
		writes []string
	)

	writeOverride := func(data []byte) (int, error) {
		mu.Lock()
		writes = append(writes, string(data))
		mu.Unlock()
		if string(data) == "\r" {
			pane.FeedOutput([]byte(codexReadyScreen("Implement {feature}")))
		}
		return len(data), nil
	}

	srv, sess, createdPane, cleanup := setupWaitReadyTestPane(t, writeOverride)
	defer cleanup()
	pane = createdPane
	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--continue-known-dialogs", "--timeout", "50ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready result = %#v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 || writes[0] != "\r" {
		t.Fatalf("dialog writes = %#v, want single Enter", writes)
	}
}

func TestWaitReadyUsesClaudeCursorBlockFallback(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(claudePromptScreen()))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "10ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready Claude result = %#v", res)
	}
}

func TestWaitReadyRejectsNumberedMenuRows(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.FeedOutput([]byte(numberedMenuScreen()))

	res := runTestCommand(t, srv, sess, "wait-ready", "pane-1", "--timeout", "1ms")
	if !strings.Contains(res.cmdErr, "timeout waiting for pane-1 to become ready") {
		t.Fatalf("wait-ready timeout = %#v", res)
	}
}

func TestSendKeysWaitReadyContinuesKnownDialogBeforeSendingInput(t *testing.T) {
	t.Parallel()

	var (
		pane   *mux.Pane
		mu     sync.Mutex
		writes []string
	)

	writeOverride := func(data []byte) (int, error) {
		mu.Lock()
		writes = append(writes, string(data))
		mu.Unlock()
		if string(data) == "\r" && len(writes) == 1 {
			pane.FeedOutput([]byte(codexReadyScreen("Summarize recent commits")))
		}
		return len(data), nil
	}

	srv, sess, createdPane, cleanup := setupWaitReadyTestPane(t, writeOverride)
	defer cleanup()
	pane = createdPane
	pane.FeedOutput([]byte(codexTrustDialogScreen("/tmp/untrusted")))

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1", "--wait-ready", "--continue-known-dialogs", "ship it", "Enter")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "Sent 8 bytes to pane-1" {
		t.Fatalf("send-keys result = %#v", res)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(writes, "|"); got != "\r|ship it|\r" {
		t.Fatalf("send-keys writes = %q, want dialog Enter + task + Enter", got)
	}
}
