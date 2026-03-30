package server

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func setupSendKeysWaitIdleTestPane(t *testing.T, writeOverride func([]byte) (int, error)) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	if writeOverride == nil {
		writeOverride = func(data []byte) (int, error) { return len(data), nil }
	}
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), writeOverride)
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, pane)
	return srv, sess, pane, cleanup
}

func TestSendKeysCommandUsageIncludesReadyAndVia(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1")
	if got := res.cmdErr; got != "usage: send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestCmdSendKeysCommandWaitReadyWaitsForReady(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	writes := make(chan string, 1)
	srv, sess, pane, cleanup := setupSendKeysWaitIdleTestPane(t, func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	})
	defer cleanup()

	sess.Clock = clk
	sess.vtIdle = NewVTIdleTracker(clk)
	sess.VTIdleSettle = 100 * time.Millisecond
	pane.SetCreatedAt(clk.Now())

	clientConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--wait", "ready", "--timeout", "5s", "ab")
	clk.AwaitTimers(2)

	select {
	case got := <-writes:
		t.Fatalf("send-keys wrote before pane became ready: %q", got)
	default:
	}

	select {
	case <-done:
		t.Fatal("send-keys returned before pane became ready")
	case <-time.After(20 * time.Millisecond):
	}

	clk.Advance(110 * time.Millisecond)

	select {
	case got := <-writes:
		if got != "ab" {
			t.Fatalf("send-keys wrote %q, want ab", got)
		}
	case <-time.After(time.Second):
		t.Fatal("send-keys did not write after pane became ready")
	}

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdOutput; got != "Sent 2 bytes to pane-1\n" {
		t.Fatalf("send-keys output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send-keys wait-ready command did not return")
	}
}

func TestSendKeysCommandWaitReadyMissingPane(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "missing", "--wait", "ready", "ship it")
	if !strings.Contains(res.cmdErr, "not found") {
		t.Fatalf("send-keys missing pane error = %q", res.cmdErr)
	}
}
