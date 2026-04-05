package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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
	if got := res.cmdErr; got != "usage: send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..." {
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
	sess.ensureIdleTracker().VTIdleSettle = 100 * time.Millisecond
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

func TestCmdSendKeysWaitsForPendingLocalPaneInputTarget(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	sess.localPaneBuilder = func(req localPaneBuildRequest) (*mux.Pane, error) {
		close(buildStarted)
		<-releaseBuild
		return defaultLocalPaneBuilder(req)
	}

	_, err := enqueueSessionQuery(sess, func(sess *Session) (ensureInitialWindowResult, error) {
		return sess.ensureInitialWindowLocked(srv, 80, 24, nil)
	})
	if err != nil {
		t.Fatalf("ensureInitialWindowLocked() error = %v", err)
	}

	clientConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "printf PENDING_OK", "Enter")

	waitForSignal(t, buildStarted, "pending local pane build")

	select {
	case <-done:
		t.Fatal("send-keys returned before pending local pane finished building")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseBuild)

	waitUntil(t, func() bool {
		snap := mustSessionQuery(t, sess, func(sess *Session) mux.CaptureSnapshot {
			pane := sess.findPaneByID(1)
			if pane == nil {
				return mux.CaptureSnapshot{}
			}
			return pane.CaptureSnapshot()
		})
		return strings.Contains(strings.Join(snap.History, "\n")+"\n"+strings.Join(snap.Content, "\n"), "PENDING_OK")
	})

	result := readUntil(t, clientConn, func(msg *Message) bool {
		return msg.Type == MsgTypeCmdResult
	})
	if result.CmdErr != "" {
		t.Fatalf("send-keys cmdErr = %q", result.CmdErr)
	}
	if got := result.CmdOutput; got != "Sent 18 bytes to pane-1\n" {
		t.Fatalf("send-keys result = %#v", result)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send-keys against pending pane did not return")
	}
}

func TestCmdSendKeysViaClientUsesRequestedClient(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	firstServerConn, firstPeerConn := net.Pipe()
	defer firstServerConn.Close()
	defer firstPeerConn.Close()

	secondServerConn, secondPeerConn := net.Pipe()
	defer secondServerConn.Close()
	defer secondPeerConn.Close()

	firstClient := newClientConn(firstServerConn)
	firstClient.ID = "client-1"
	firstClient.inputIdle = true
	firstClient.uiGeneration = 1
	firstClient.initTypeKeyQueue()
	defer firstClient.Close()

	secondClient := newClientConn(secondServerConn)
	secondClient.ID = "client-2"
	secondClient.inputIdle = true
	secondClient.uiGeneration = 1
	secondClient.initTypeKeyQueue()
	defer secondClient.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(firstClient, secondClient)
		return struct{}{}
	})

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--via", "client", "--client", "client-2", "ab")

	select {
	case <-done:
		t.Fatal("send-keys returned before fresh input-idle on requested client")
	case <-time.After(20 * time.Millisecond):
	}

	typeKeysMsg := readMsgWithTimeout(t, secondPeerConn)
	if typeKeysMsg.Type != MsgTypeTypeKeys || typeKeysMsg.PaneID != 1 || string(typeKeysMsg.Input) != "ab" {
		t.Fatalf("send-keys type-keys message = %#v", typeKeysMsg)
	}

	sess.enqueueUIEvent(secondClient, proto.UIEventInputBusy)
	sess.enqueueUIEvent(secondClient, proto.UIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Sent 2 bytes to pane-1\n" {
		t.Fatalf("send-keys output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send-keys via-client command did not return")
	}
}

func TestCmdSendKeysWaitInputIdleUsesRequestedClient(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	firstServerConn, firstPeerConn := net.Pipe()
	defer firstServerConn.Close()
	defer firstPeerConn.Close()

	secondServerConn, secondPeerConn := net.Pipe()
	defer secondServerConn.Close()
	defer secondPeerConn.Close()

	firstClient := newClientConn(firstServerConn)
	firstClient.ID = "client-1"
	firstClient.inputIdle = true
	firstClient.uiGeneration = 1
	firstClient.initTypeKeyQueue()
	defer firstClient.Close()

	secondClient := newClientConn(secondServerConn)
	secondClient.ID = "client-2"
	secondClient.inputIdle = true
	secondClient.uiGeneration = 1
	secondClient.initTypeKeyQueue()
	defer secondClient.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(firstClient, secondClient)
		return struct{}{}
	})

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--wait", "ui=input-idle", "--client", "client-2", "--timeout", "100ms", "ab")

	select {
	case <-done:
		t.Fatal("send-keys returned before fresh input-idle on requested client")
	case <-time.After(20 * time.Millisecond):
	}

	typeKeysMsg := readMsgWithTimeout(t, secondPeerConn)
	if typeKeysMsg.Type != MsgTypeTypeKeys || typeKeysMsg.PaneID != 1 || string(typeKeysMsg.Input) != "ab" {
		t.Fatalf("send-keys type-keys message = %#v", typeKeysMsg)
	}

	sess.enqueueUIEvent(secondClient, proto.UIEventInputBusy)
	sess.enqueueUIEvent(secondClient, proto.UIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Sent 2 bytes to pane-1\n" {
		t.Fatalf("send-keys output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send-keys wait-input-idle command did not return")
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
