package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func runOneShotCommand(t *testing.T, sess *Session, args []string, fn func(*CommandContext)) *Message {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	cc := newClientConn(serverConn)
	cc.ID = "cmd-client"

	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(&CommandContext{CC: cc, Sess: sess, Args: args})
	}()

	msg := readMsgWithTimeout(t, peerConn)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("command did not return after reply")
	}

	cc.Close()
	_ = peerConn.Close()
	_ = serverConn.Close()
	return msg
}

func readCmdResultEvent(t *testing.T, conn net.Conn) Event {
	t.Helper()

	return readCmdResultEventWithTimeout(t, conn, time.Second)
}

func readCmdResultEventWithTimeout(t *testing.T, conn net.Conn, timeout time.Duration) Event {
	t.Helper()

	msg := readMsgWithTimeoutDuration(t, conn, timeout)
	if msg.Type != MsgTypeCmdResult {
		t.Fatalf("message type = %v, want cmd result", msg.Type)
	}

	var ev Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(msg.CmdOutput)), &ev); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", msg.CmdOutput, err)
	}
	return ev
}

func TestParseTypeKeysArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    typeKeysOptions
		wantErr string
	}{
		{
			name: "wait and timeout",
			args: []string{"--wait", "ui=input-idle", "--timeout", "25ms", "--hex", "6162"},
			want: typeKeysOptions{
				waitInputIdle: true,
				waitTimeout:   25 * time.Millisecond,
				hexMode:       true,
				keys:          []string{"6162"},
			},
		},
		{
			name: "literal args after first key",
			args: []string{"hello", "--wait", "ui=input-idle"},
			want: typeKeysOptions{
				waitTimeout: defaultCommandUIWaitTimeout,
				keys:        []string{"hello", "--wait", "ui=input-idle"},
			},
		},
		{
			name:    "missing wait value",
			args:    []string{"--wait"},
			wantErr: "missing value for --wait",
		},
		{
			name:    "unsupported wait target",
			args:    []string{"--wait", "ready", "hello"},
			wantErr: `type-keys: unsupported --wait target "ready" (want ui=input-idle)`,
		},
		{
			name:    "missing timeout value",
			args:    []string{"--wait", "ui=input-idle", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--wait", "ui=input-idle", "--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "timeout requires wait",
			args:    []string{"--timeout", "10ms", "hello"},
			wantErr: "type-keys: --timeout requires --wait ui=input-idle",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTypeKeysArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseTypeKeysArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTypeKeysArgs(%v): %v", tt.args, err)
			}
			if got.waitInputIdle != tt.want.waitInputIdle ||
				got.waitTimeout != tt.want.waitTimeout ||
				got.hexMode != tt.want.hexMode ||
				strings.Join(got.keys, "|") != strings.Join(tt.want.keys, "|") {
				t.Fatalf("parsed = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseCopyModeArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    copyModeOptions
		wantErr string
	}{
		{
			name: "pane wait and timeout",
			args: []string{"pane-2", "--wait", "ui=copy-mode-shown", "--timeout", "40ms"},
			want: copyModeOptions{
				paneRef:           "pane-2",
				waitCopyModeShown: true,
				waitTimeout:       40 * time.Millisecond,
			},
		},
		{
			name: "active pane defaults",
			args: nil,
			want: copyModeOptions{waitTimeout: defaultCommandUIWaitTimeout},
		},
		{
			name:    "missing wait value",
			args:    []string{"--wait"},
			wantErr: "missing value for --wait",
		},
		{
			name:    "unsupported wait target",
			args:    []string{"--wait", "ready"},
			wantErr: `copy-mode: unsupported --wait target "ready" (want ui=copy-mode-shown)`,
		},
		{
			name:    "missing timeout value",
			args:    []string{"--wait", "ui=copy-mode-shown", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--wait", "ui=copy-mode-shown", "--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "unknown flag",
			args:    []string{"--bogus"},
			wantErr: "unknown flag: --bogus",
		},
		{
			name:    "multiple pane refs",
			args:    []string{"pane-1", "pane-2"},
			wantErr: copyModeUsage,
		},
		{
			name:    "timeout requires wait",
			args:    []string{"--timeout", "10ms"},
			wantErr: "copy-mode: --timeout requires --wait ui=copy-mode-shown",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCopyModeArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseCopyModeArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCopyModeArgs(%v): %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("parsed = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCmdTypeKeysWaitsForInputIdle(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	uiServerConn, uiPeerConn := net.Pipe()
	defer uiServerConn.Close()
	defer uiPeerConn.Close()

	uiClient := newClientConn(uiServerConn)
	uiClient.ID = "client-1"
	uiClient.inputIdle = true
	uiClient.uiGeneration = 1
	uiClient.setNegotiatedCapabilities(proto.ClientCapabilities{KittyKeyboard: true, Hyperlinks: true})
	uiClient.initTypeKeyQueue()
	defer uiClient.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(uiClient)
		return struct{}{}
	})

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "type-keys", "--wait", "ui=input-idle", "--timeout", "100ms", "ab")

	typeKeysMsg := readMsgWithTimeout(t, uiPeerConn)
	if typeKeysMsg.Type != MsgTypeTypeKeys || string(typeKeysMsg.Input) != "ab" {
		t.Fatalf("type-keys message = %#v", typeKeysMsg)
	}

	select {
	case <-done:
		t.Fatal("type-keys returned before fresh input-idle")
	case <-time.After(20 * time.Millisecond):
	}

	sess.enqueueUIEvent(uiClient, proto.UIEventInputBusy)
	sess.enqueueUIEvent(uiClient, proto.UIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Typed 2 bytes\n" {
		t.Fatalf("type-keys output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("type-keys wait command did not return")
	}
}

func TestCmdSendKeysShowsUsageWhenNoKeysProvided(t *testing.T) {
	t.Parallel()

	sess := newSession("test-send-keys-no-keys")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	msg := runOneShotCommand(t, sess, []string{"pane-1", "--hex"}, cmdSendKeys)
	if got := msg.CmdErr; got != sendKeysUsage {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestCmdSendKeysWaitsForInputIdle(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	writes := make(chan string, 1)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	})
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}

	uiServerConn, uiPeerConn := net.Pipe()
	defer uiServerConn.Close()
	defer uiPeerConn.Close()

	uiClient := newClientConn(uiServerConn)
	uiClient.ID = "client-1"
	uiClient.inputIdle = true
	uiClient.uiGeneration = 1
	uiClient.initTypeKeyQueue()
	defer uiClient.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(uiClient)
		return struct{}{}
	})

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--wait", "ui=input-idle", "--timeout", "100ms", "ab")

	typeKeysMsg := readMsgWithTimeout(t, uiPeerConn)
	if typeKeysMsg.Type != MsgTypeTypeKeys || string(typeKeysMsg.Input) != "ab" {
		t.Fatalf("send-keys type-keys message = %#v", typeKeysMsg)
	}

	select {
	case got := <-writes:
		t.Fatalf("send-keys --wait ui=input-idle should route through client, wrote directly to pane: %q", got)
	default:
	}

	select {
	case <-done:
		t.Fatal("send-keys returned before fresh input-idle")
	case <-time.After(20 * time.Millisecond):
	}

	sess.enqueueUIEvent(uiClient, proto.UIEventInputBusy)
	sess.enqueueUIEvent(uiClient, proto.UIEventInputIdle)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Sent 2 bytes to pane-1\n" {
		t.Fatalf("send-keys output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("send-keys wait command did not return")
	}
}

func TestCmdTypeKeysErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-type-keys-parse-error")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, []string{"--wait"}, cmdTypeKeys)
		if got := msg.CmdErr; got != "missing value for --wait" {
			t.Fatalf("type-keys parse error = %q", got)
		}
	})

	t.Run("usage when no keys provided", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-type-keys-no-keys")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, []string{"--hex"}, cmdTypeKeys)
		if got := msg.CmdErr; got != typeKeysUsage {
			t.Fatalf("type-keys usage error = %q", got)
		}
	})

	t.Run("wait requires attached client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-type-keys-no-client")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, []string{"--wait", "ui=input-idle", "--timeout", "25ms", "ab"}, cmdTypeKeys)
		if got := msg.CmdErr; got != "no client attached" {
			t.Fatalf("type-keys no-client error = %q", got)
		}
	})

	t.Run("wait times out without fresh idle", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		uiServerConn, uiPeerConn := net.Pipe()
		defer uiServerConn.Close()
		defer uiPeerConn.Close()

		uiClient := newClientConn(uiServerConn)
		uiClient.ID = "client-1"
		uiClient.inputIdle = true
		uiClient.uiGeneration = 1
		uiClient.initTypeKeyQueue()
		defer uiClient.Close()

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(uiClient)
			return struct{}{}
		})

		cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "type-keys", "--wait", "ui=input-idle", "--timeout", "25ms", "ab")

		typeKeysMsg := readMsgWithTimeout(t, uiPeerConn)
		if typeKeysMsg.Type != MsgTypeTypeKeys || string(typeKeysMsg.Input) != "ab" {
			t.Fatalf("type-keys message = %#v", typeKeysMsg)
		}

		result := readMsgWithTimeout(t, cmdPeerConn)
		if got := result.CmdErr; got != "timeout waiting for input-idle on client-1" {
			t.Fatalf("type-keys timeout error = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("type-keys timeout command did not return")
		}
	})
}

func TestCmdCopyModeWaitsForShown(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	uiServerConn, uiPeerConn := net.Pipe()
	defer uiServerConn.Close()
	defer uiPeerConn.Close()

	uiClient := newClientConn(uiServerConn)
	uiClient.ID = "client-1"
	uiClient.uiGeneration = 2
	defer uiClient.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(uiClient)
		return struct{}{}
	})

	cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "copy-mode", "pane-1", "--wait", "ui=copy-mode-shown", "--timeout", "100ms")

	copyModeMsg := readMsgWithTimeout(t, uiPeerConn)
	if copyModeMsg.Type != MsgTypeCopyMode || copyModeMsg.PaneID != 1 {
		t.Fatalf("copy-mode message = %#v", copyModeMsg)
	}

	select {
	case <-done:
		t.Fatal("copy-mode returned before copy-mode-shown")
	case <-time.After(20 * time.Millisecond):
	}

	sess.enqueueUIEvent(uiClient, proto.UIEventCopyModeShown)

	result := readMsgWithTimeout(t, cmdPeerConn)
	if got := result.CmdOutput; got != "Copy mode entered for pane-1\n" {
		t.Fatalf("copy-mode output = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("copy-mode wait command did not return")
	}
}

func TestCmdCopyModeUsesActivePaneByDefault(t *testing.T) {
	t.Parallel()

	_, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	msg := runOneShotCommand(t, sess, nil, cmdCopyMode)
	if got := msg.CmdOutput; got != "Copy mode entered for pane-1\n" {
		t.Fatalf("copy-mode default pane output = %q", got)
	}
}

func TestCmdCopyModeErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("parse error", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-copy-mode-parse-error")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, []string{"--wait"}, cmdCopyMode)
		if got := msg.CmdErr; got != "missing value for --wait" {
			t.Fatalf("copy-mode parse error = %q", got)
		}
	})

	t.Run("wait requires attached client", func(t *testing.T) {
		t.Parallel()

		_, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
		defer cleanup()

		msg := runOneShotCommand(t, sess, []string{"pane-1", "--wait", "ui=copy-mode-shown", "--timeout", "25ms"}, cmdCopyMode)
		if got := msg.CmdErr; got != "no client attached" {
			t.Fatalf("copy-mode no-client error = %q", got)
		}
	})

	t.Run("wait times out without copy-mode-shown", func(t *testing.T) {
		t.Parallel()

		srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
		defer cleanup()

		uiServerConn, uiPeerConn := net.Pipe()
		defer uiServerConn.Close()
		defer uiPeerConn.Close()

		uiClient := newClientConn(uiServerConn)
		uiClient.ID = "client-1"
		uiClient.uiGeneration = 1
		defer uiClient.Close()

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(uiClient)
			return struct{}{}
		})

		cmdPeerConn, _, done := startAsyncCommand(t, srv, sess, "copy-mode", "pane-1", "--wait", "ui=copy-mode-shown", "--timeout", "25ms")

		copyModeMsg := readMsgWithTimeout(t, uiPeerConn)
		if copyModeMsg.Type != MsgTypeCopyMode || copyModeMsg.PaneID != 1 {
			t.Fatalf("copy-mode message = %#v", copyModeMsg)
		}

		result := readMsgWithTimeout(t, cmdPeerConn)
		if got := result.CmdErr; got != "timeout waiting for copy-mode-shown on client-1" {
			t.Fatalf("copy-mode timeout error = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("copy-mode timeout command did not return")
		}
	})
}

func TestMetaCollectionCommandsUsageAndErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantSubstr string
	}{
		{
			name:       "add-meta usage",
			cmd:        "add-meta",
			wantSubstr: "usage: add-meta <pane> key=value [key=value...]",
		},
		{
			name:       "add-meta invalid keyvalue",
			cmd:        "add-meta",
			args:       []string{"pane-1", "nope"},
			wantSubstr: "invalid key=value",
		},
		{
			name:       "add-meta invalid pr",
			cmd:        "add-meta",
			args:       []string{"pane-1", "pr=abc"},
			wantSubstr: "invalid pr value",
		},
		{
			name:       "add-meta invalid issue",
			cmd:        "add-meta",
			args:       []string{"pane-1", "issue="},
			wantSubstr: "invalid issue value",
		},
		{
			name:       "add-meta unknown key",
			cmd:        "add-meta",
			args:       []string{"pane-1", "task=ship"},
			wantSubstr: "unknown meta key",
		},
		{
			name:       "add-meta unknown pane",
			cmd:        "add-meta",
			args:       []string{"no-such-pane", "pr=1"},
			wantSubstr: "not found",
		},
		{
			name:       "rm-meta usage",
			cmd:        "rm-meta",
			wantSubstr: "usage: rm-meta <pane> key=value [key=value...]",
		},
		{
			name:       "rm-meta invalid keyvalue",
			cmd:        "rm-meta",
			args:       []string{"pane-1", "nope"},
			wantSubstr: "invalid key=value",
		},
		{
			name:       "rm-meta unknown pane",
			cmd:        "rm-meta",
			args:       []string{"no-such-pane", "pr=1"},
			wantSubstr: "not found",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			pane := newTestPane(sess, 1, "pane-1")
			window := newTestWindowWithPanes(t, sess, 1, "main", pane)
			sess.Windows = []*mux.Window{window}
			sess.ActiveWindowID = window.ID
			sess.Panes = []*mux.Pane{pane}

			res := runTestCommand(t, srv, sess, tt.cmd, tt.args...)
			if !strings.Contains(res.cmdErr, tt.wantSubstr) {
				t.Fatalf("%s error = %q, want substring %q", tt.name, res.cmdErr, tt.wantSubstr)
			}
		})
	}
}

func TestKillCommandUsageAndSubscriptionCleanup(t *testing.T) {
	t.Parallel()

	t.Run("usage and parse errors", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			args       []string
			wantSubstr string
		}{
			{
				name:       "timeout requires cleanup",
				args:       []string{"--timeout", "1s"},
				wantSubstr: "usage: kill [--cleanup] [--timeout <duration>] [pane]",
			},
			{
				name:       "invalid timeout",
				args:       []string{"--cleanup", "--timeout", "bogus", "pane-1"},
				wantSubstr: "invalid timeout: bogus",
			},
			{
				name:       "extra pane argument",
				args:       []string{"pane-1", "pane-2"},
				wantSubstr: "usage: kill [--cleanup] [--timeout <duration>] [pane]",
			},
			{
				name:       "unknown flag",
				args:       []string{"--bogus"},
				wantSubstr: "unknown flag: --bogus",
			},
		}

		for _, tt := range tests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				srv, sess, cleanup := newCommandTestSession(t)
				defer cleanup()

				pane1 := newTestPane(sess, 1, "pane-1")
				pane2 := newTestPane(sess, 2, "pane-2")
				window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
				sess.Windows = []*mux.Window{window}
				sess.ActiveWindowID = window.ID
				sess.Panes = []*mux.Pane{pane1, pane2}

				res := runTestCommand(t, srv, sess, "kill", tt.args...)
				if !strings.Contains(res.cmdErr, tt.wantSubstr) {
					t.Fatalf("kill error = %q, want substring %q", res.cmdErr, tt.wantSubstr)
				}
			})
		}
	})

	t.Run("plain kill prunes pane subscriptions", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane1 := newTestPane(sess, 1, "pane-1")
		pane2 := newTestPane(sess, 2, "pane-2")
		window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2}

		sub := sess.enqueueEventSubscribe(eventFilter{PaneName: "pane-2"}, false)
		waitCh := sess.enqueuePaneOutputSubscribe(pane2.ID)
		defer sess.enqueueEventUnsubscribe(sub.sub)
		defer sess.enqueuePaneOutputUnsubscribe(pane2.ID, waitCh)

		res := runTestCommand(t, srv, sess, "kill", "pane-2")
		if res.cmdErr != "" || !strings.Contains(res.output, "Killed pane-2") {
			t.Fatalf("kill result = %#v", res)
		}

		state := mustSessionQuery(t, sess, func(sess *Session) struct {
			hasEventSub      bool
			outputSubCount   int
			remainingPaneCnt int
		} {
			hasEventSub := false
			for _, existing := range sess.eventSubs {
				if existing.filter.PaneName == "pane-2" {
					hasEventSub = true
					break
				}
			}
			return struct {
				hasEventSub      bool
				outputSubCount   int
				remainingPaneCnt int
			}{
				hasEventSub:      hasEventSub,
				outputSubCount:   sess.waiters.outputSubscriberCount(pane2.ID),
				remainingPaneCnt: len(sess.Panes),
			}
		})
		if state.hasEventSub {
			t.Fatal("pane-specific event subscription should be removed when the pane is killed")
		}
		if state.outputSubCount != 0 {
			t.Fatalf("pane output subscribers after kill = %d, want 0", state.outputSubCount)
		}
		if state.remainingPaneCnt != 1 {
			t.Fatalf("pane count after kill = %d, want 1", state.remainingPaneCnt)
		}
	})
}

func TestCmdWaitUIUnknownImmediateAndTimeout(t *testing.T) {
	t.Parallel()

	t.Run("unknown event", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-wait-ui-unknown")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, []string{"not-a-ui-event"}, cmdWaitUI)
		if !strings.Contains(msg.CmdErr, "unknown ui event") {
			t.Fatalf("cmdWaitUI error = %q, want unknown ui event", msg.CmdErr)
		}
	})

	t.Run("current match returns immediately", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-wait-ui-current")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		serverConn, peerConn := net.Pipe()
		cc := newClientConn(serverConn)
		cc.ID = "client-1"
		cc.copyModeShown = true
		cc.uiGeneration = 3
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(cc)
			return struct{}{}
		})

		done := make(chan struct{})
		go func() {
			defer close(done)
			cmdWaitUI(&CommandContext{CC: cc, Sess: sess, Args: []string{"copy-mode-shown", "--after", "2"}})
		}()

		msg := readMsgWithTimeout(t, peerConn)
		if got := msg.CmdOutput; got != "copy-mode-shown\n" {
			t.Fatalf("cmdWaitUI immediate output = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cmdWaitUI immediate case did not return")
		}

		cc.Close()
		_ = peerConn.Close()
		_ = serverConn.Close()
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-wait-ui-timeout")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		serverConn, peerConn := net.Pipe()
		cc := newClientConn(serverConn)
		cc.ID = "client-2"
		cc.inputIdle = true
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(cc)
			return struct{}{}
		})

		done := make(chan struct{})
		go func() {
			defer close(done)
			cmdWaitUI(&CommandContext{CC: cc, Sess: sess, Args: []string{"input-busy", "--timeout", "20ms"}})
		}()

		msg := readMsgWithTimeout(t, peerConn)
		if got := msg.CmdErr; got != "timeout waiting for input-busy on client-2" {
			t.Fatalf("cmdWaitUI timeout error = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cmdWaitUI timeout case did not return")
		}

		cc.Close()
		_ = peerConn.Close()
		_ = serverConn.Close()
	})
}

func TestCmdListClientsFormatsClientsAndEmptyState(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-list-clients-empty")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, nil, cmdListClients)
		if got := msg.CmdOutput; got != "No clients attached.\n" {
			t.Fatalf("cmdListClients empty output = %q", got)
		}
	})

	t.Run("formats active clients", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-list-clients-full")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		cc1 := &clientConn{
			ID:                "client-1",
			displayPanesShown: true,
			cols:              80,
			rows:              24,
			capabilities:      proto.ClientCapabilities{Hyperlinks: true},
			inputIdle:         true,
		}
		cc2 := &clientConn{
			ID:          "client-2",
			chooserMode: chooserWindow,
			cols:        60,
			rows:        20,
			inputIdle:   true,
		}
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(cc1, cc2)
			sess.ensureClientManager().setSizeOwnerForTest(cc2)
			return struct{}{}
		})

		msg := runOneShotCommand(t, sess, nil, cmdListClients)
		for _, want := range []string{"CLIENT", "client-1", "client-2", "80x24", "60x20", "shown", "window", "hyperlinks", "*"} {
			if !strings.Contains(msg.CmdOutput, want) {
				t.Fatalf("cmdListClients missing %q:\n%s", want, msg.CmdOutput)
			}
		}
	})
}

func TestRemoteHostCommandsReportConfiguredAndErrorStates(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Hosts: map[string]config.Host{
			"dev":   {Type: "remote", Address: "example.com:22"},
			"local": {Type: "local"},
		},
	}

	t.Run("hosts without manager", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-hosts-none")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		msg := runOneShotCommand(t, sess, nil, cmdHosts)
		if got := msg.CmdOutput; got != "No remote hosts configured.\n" {
			t.Fatalf("cmdHosts without manager = %q", got)
		}
	})

	t.Run("hosts with configured manager", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-hosts-configured")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)
		sess.SetupRemoteManager(cfg, "")
		defer sess.RemoteManager.Shutdown()

		msg := runOneShotCommand(t, sess, nil, cmdHosts)
		for _, want := range []string{"HOST", "STATUS", "dev", "disconnected"} {
			if !strings.Contains(msg.CmdOutput, want) {
				t.Fatalf("cmdHosts missing %q:\n%s", want, msg.CmdOutput)
			}
		}
	})

	t.Run("disconnect and reconnect error paths", func(t *testing.T) {
		t.Parallel()

		noMgr := newSession("test-host-commands-no-manager")
		stopCrashCheckpointLoop(t, noMgr)
		defer stopSessionBackgroundLoops(t, noMgr)

		msg := runOneShotCommand(t, noMgr, nil, cmdDisconnect)
		if got := msg.CmdErr; got != "usage: disconnect <host>" {
			t.Fatalf("disconnect usage error = %q", got)
		}

		msg = runOneShotCommand(t, noMgr, []string{"dev"}, cmdDisconnect)
		if got := msg.CmdErr; got != "no remote hosts configured" {
			t.Fatalf("disconnect no-manager error = %q", got)
		}

		msg = runOneShotCommand(t, noMgr, nil, cmdReconnect)
		if got := msg.CmdErr; got != "usage: reconnect <host>" {
			t.Fatalf("reconnect usage error = %q", got)
		}

		msg = runOneShotCommand(t, noMgr, []string{"dev"}, cmdReconnect)
		if got := msg.CmdErr; got != "no remote hosts configured" {
			t.Fatalf("reconnect no-manager error = %q", got)
		}

		withMgr := newSession("test-host-commands-configured")
		stopCrashCheckpointLoop(t, withMgr)
		defer stopSessionBackgroundLoops(t, withMgr)
		withMgr.SetupRemoteManager(cfg, "")
		defer withMgr.RemoteManager.Shutdown()

		msg = runOneShotCommand(t, withMgr, []string{"dev"}, cmdDisconnect)
		if got := msg.CmdErr; got != "host \"dev\" not connected" {
			t.Fatalf("disconnect configured error = %q", got)
		}

		msg = runOneShotCommand(t, withMgr, []string{"dev"}, cmdReconnect)
		if got := msg.CmdErr; got != "host \"dev\" not known" {
			t.Fatalf("reconnect configured error = %q", got)
		}
	})
}

func TestCmdEventsStreamsAndThrottlesOutput(t *testing.T) {
	t.Parallel()

	drainInitialPaneState := func(t *testing.T, conn net.Conn, paneCount int) {
		t.Helper()

		seen := make(map[uint32]bool, paneCount)
		for len(seen) < paneCount {
			ev := readCmdResultEvent(t, conn)
			if ev.Type == EventBusy || ev.Type == EventIdle {
				seen[ev.PaneID] = true
			}
		}
	}

	t.Run("no throttle streams output immediately", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-events-immediate")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, nil, nil, func(data []byte) (int, error) {
			return len(data), nil
		})
		sess.Panes = []*mux.Pane{pane}

		serverConn, peerConn := net.Pipe()
		cc := newClientConn(serverConn)
		done := make(chan struct{})
		go func() {
			defer close(done)
			cmdEvents(&CommandContext{CC: cc, Sess: sess, Args: []string{"--throttle", "0s"}})
		}()

		drainInitialPaneState(t, peerConn, 1)

		sess.paneOutputCallback()(pane.ID, []byte("hello"), 1)
		var ev Event
		for {
			ev = readCmdResultEvent(t, peerConn)
			if ev.Type == EventOutput {
				break
			}
		}
		if ev.Type != EventOutput || ev.PaneID != pane.ID || ev.PaneName != "pane-1" {
			t.Fatalf("output event = %+v", ev)
		}

		_ = peerConn.Close()
		sess.paneOutputCallback()(pane.ID, []byte("again"), 2)

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cmdEvents immediate stream did not exit after client disconnect")
		}

		cc.Close()
		_ = serverConn.Close()
	})

	t.Run("throttle coalesces output events by pane", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-events-throttle")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		pane1 := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, nil, nil, func(data []byte) (int, error) {
			return len(data), nil
		})
		pane2 := newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: config.AccentColor(1)}, 80, 23, nil, nil, func(data []byte) (int, error) {
			return len(data), nil
		})
		sess.Panes = []*mux.Pane{pane1, pane2}

		serverConn, peerConn := net.Pipe()
		cc := newClientConn(serverConn)
		done := make(chan struct{})
		go func() {
			defer close(done)
			cmdEvents(&CommandContext{CC: cc, Sess: sess, Args: []string{"--throttle", "20ms"}})
		}()

		drainInitialPaneState(t, peerConn, 2)

		sess.paneOutputCallback()(pane2.ID, []byte("pane2"), 1)
		sess.paneOutputCallback()(pane1.ID, []byte("pane1"), 1)
		sess.paneOutputCallback()(pane1.ID, []byte("pane1 again"), 2)

		var outputs []Event
		for len(outputs) < 2 {
			ev := readCmdResultEvent(t, peerConn)
			if ev.Type != EventOutput {
				continue
			}
			outputs = append(outputs, ev)
		}
		first := outputs[0]
		second := outputs[1]
		if first.Type != EventOutput || second.Type != EventOutput {
			t.Fatalf("expected output events, got %+v and %+v", first, second)
		}
		ids := [2]uint32{first.PaneID, second.PaneID}
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
		if ids != [2]uint32{pane1.ID, pane2.ID} {
			t.Fatalf("throttled pane IDs = [%d %d], want panes [%d %d]", first.PaneID, second.PaneID, pane1.ID, pane2.ID)
		}

		_ = peerConn.Close()
		sess.paneOutputCallback()(pane1.ID, []byte("final"), 3)

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cmdEvents throttled stream did not exit after client disconnect")
		}

		cc.Close()
		_ = serverConn.Close()
	})
}
