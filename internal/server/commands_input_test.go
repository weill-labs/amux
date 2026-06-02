package server

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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
	if got := res.cmdErr; got != "usage: send-keys (<pane>|--window <index|name>) [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--submit] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestCmdSendKeysSubmitWrapsBodyAsBracketedPasteAndEnter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		body string
	}{
		{
			name: "literal body",
			args: []string{"--submit", "first line\nsecond line"},
			body: "first line\nsecond line",
		},
		{
			name: "hex body",
			args: []string{"--submit", "--hex", hex.EncodeToString([]byte("first line\nsecond line"))},
			body: "first line\nsecond line",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			writes := make(chan string, 2)
			srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, func(data []byte) (int, error) {
				writes <- string(data)
				return len(data), nil
			})
			defer cleanup()

			paste := ansi.BracketedPasteStart + tt.body + ansi.BracketedPasteEnd
			args := append([]string{"pane-1"}, tt.args...)
			res := runTestCommand(t, srv, sess, "send-keys", args...)
			if res.cmdErr != "" {
				t.Fatalf("send-keys --submit cmdErr = %q", res.cmdErr)
			}
			if got, want := res.output, fmt.Sprintf("Submitted %d bytes to pane-1\n", len(paste)+1); got != want {
				t.Fatalf("send-keys --submit output = %q, want %q", got, want)
			}

			if got := <-writes; got != paste {
				t.Fatalf("first write = %q, want bracketed paste %q", got, paste)
			}
			if got := <-writes; got != "\r" {
				t.Fatalf("second write = %q, want carriage return", got)
			}
		})
	}
}

func TestCmdSendKeysSubmitReportsInvalidHexWithoutWriting(t *testing.T) {
	t.Parallel()

	writes := make(chan string, 1)
	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	})
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1", "--submit", "--hex", "zz")
	if got := res.cmdErr; got != "invalid hex: zz" {
		t.Fatalf("send-keys --submit invalid hex error = %q", got)
	}
	select {
	case got := <-writes:
		t.Fatalf("send-keys --submit invalid hex wrote %q", got)
	default:
	}
}

func TestCmdSendKeysRemoteTargetForwardsWithoutMirror(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	beforePanes := mustSessionQuery(t, h.localSess, func(sess *Session) int {
		return len(sess.Panes)
	})

	res := runTestCommand(t, h.localSrv, h.localSess, "send-keys", "remote:remote-agent", "remote-input")
	if res.cmdErr != "" {
		t.Fatalf("remote send-keys cmdErr = %q", res.cmdErr)
	}
	if got, want := res.output, "Sent 12 bytes to remote:remote-agent\n"; got != want {
		t.Fatalf("remote send-keys output = %q, want %q", got, want)
	}

	select {
	case got := <-h.remoteWrites:
		if string(got) != "remote-input" {
			t.Fatalf("remote input = %q, want remote-input", got)
		}
	case <-time.After(time.Second):
		t.Fatal("remote input did not reach remote pane")
	}

	afterPanes := mustSessionQuery(t, h.localSess, func(sess *Session) int {
		return len(sess.Panes)
	})
	if afterPanes != beforePanes {
		t.Fatalf("local pane count changed from %d to %d; remote send should not create a mirror", beforePanes, afterPanes)
	}
}

func TestCmdSendKeysRemoteTargetUnreachableFailsLoudly(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()
	h.dialer.fail.Store(true)

	res := runTestCommand(t, h.localSrv, h.localSess, "send-keys", "remote:remote-agent", "x")
	if res.cmdErr == "" || !strings.Contains(res.cmdErr, "remote server unavailable") {
		t.Fatalf("remote send-keys unreachable cmdErr = %q, want dial failure", res.cmdErr)
	}

	select {
	case got := <-h.remoteWrites:
		t.Fatalf("remote send-keys wrote despite dial failure: %q", got)
	default:
	}
}

func TestCmdSendKeysUnknownRemotePaneFailsLoudly(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	res := runTestCommand(t, h.localSrv, h.localSess, "send-keys", "remote:missing-agent", "x")
	if res.cmdErr == "" || !strings.Contains(res.cmdErr, `pane name "missing-agent" not found`) {
		t.Fatalf("remote send-keys missing pane cmdErr = %q, want missing remote pane", res.cmdErr)
	}

	select {
	case got := <-h.remoteWrites:
		t.Fatalf("remote send-keys wrote despite missing remote pane: %q", got)
	default:
	}
}

func TestCmdSendKeysRemoteTargetRejectsClientTransport(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	res := runTestCommand(t, h.localSrv, h.localSess, "send-keys", "remote:remote-agent", "--via", "client", "x")
	if res.cmdErr != "send-keys: remote targets support only --via pty" {
		t.Fatalf("remote send-keys --via client cmdErr = %q", res.cmdErr)
	}

	select {
	case got := <-h.remoteWrites:
		t.Fatalf("remote send-keys wrote despite unsupported transport: %q", got)
	default:
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

	_, err := enqueueSessionQueryOnState(sess.context(), sess, func(sess *Session) (ensureInitialWindowResult, error) {
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

func TestSendKeysSkipsAutomaticEnterPacingWhenForegroundIdle(t *testing.T) {
	t.Parallel()

	writes := make(chan string, 2)
	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	})
	defer cleanup()

	start := time.Now()
	res := runTestCommand(t, srv, sess, "send-keys", "pane-1", "HELLO", "Enter")
	elapsed := time.Since(start)

	if res.cmdErr != "" {
		t.Fatalf("send-keys cmdErr = %q", res.cmdErr)
	}
	if got := res.output; got != "Sent 6 bytes to pane-1\n" {
		t.Fatalf("send-keys output = %q", got)
	}
	if elapsed >= 40*time.Millisecond {
		t.Fatalf("send-keys took %v for an idle pane; automatic Enter pacing should not apply", elapsed)
	}

	if got := <-writes; got != "HELLO" {
		t.Fatalf("first write = %q, want %q", got, "HELLO")
	}
	if got := <-writes; got != "\r" {
		t.Fatalf("second write = %q, want carriage return", got)
	}
}

func TestDisableAutomaticEnterPacingForIdlePaneKeepsControlPacing(t *testing.T) {
	t.Parallel()

	chunks, err := encodeKeyChunks(false, []string{"HELLO", "Enter", "C-c"})
	if err != nil {
		t.Fatalf("encodeKeyChunks() error = %v", err)
	}

	disableAutomaticEnterPacingForIdlePane(chunks, &mux.Pane{})

	if chunks[1].paceBefore {
		t.Fatalf("Enter chunk pacing = %v, want false", chunks[1].paceBefore)
	}
	if !chunks[2].paceBefore {
		t.Fatalf("control chunk pacing = %v, want true", chunks[2].paceBefore)
	}
}
