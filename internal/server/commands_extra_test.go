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

	msg := readMsgWithTimeout(t, conn)
	if msg.Type != MsgTypeCmdResult {
		t.Fatalf("message type = %v, want cmd result", msg.Type)
	}

	var ev Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(msg.CmdOutput)), &ev); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", msg.CmdOutput, err)
	}
	return ev
}

func TestHookCommandsRoundTrip(t *testing.T) {
	t.Parallel()

	sess := newSession("test-hook-commands")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	msg := runOneShotCommand(t, sess, []string{"on-idle", "echo", "one"}, cmdSetHook)
	if got := msg.CmdOutput; got != "Hook added: on-idle → echo one\n" {
		t.Fatalf("set-hook output = %q", got)
	}

	msg = runOneShotCommand(t, sess, []string{"on-idle", "echo", "two"}, cmdSetHook)
	if got := msg.CmdOutput; got != "Hook added: on-idle → echo two\n" {
		t.Fatalf("second set-hook output = %q", got)
	}

	msg = runOneShotCommand(t, sess, nil, cmdListHooks)
	for _, want := range []string{"on-idle:", "0: echo one", "1: echo two"} {
		if !strings.Contains(msg.CmdOutput, want) {
			t.Fatalf("list-hooks missing %q:\n%s", want, msg.CmdOutput)
		}
	}

	msg = runOneShotCommand(t, sess, []string{"on-idle", "0"}, cmdUnsetHook)
	if got := msg.CmdOutput; got != "Removed hook 0 for on-idle\n" {
		t.Fatalf("unset-hook index output = %q", got)
	}

	msg = runOneShotCommand(t, sess, nil, cmdListHooks)
	if strings.Contains(msg.CmdOutput, "echo one") || !strings.Contains(msg.CmdOutput, "echo two") {
		t.Fatalf("list-hooks after indexed removal = %q", msg.CmdOutput)
	}

	msg = runOneShotCommand(t, sess, []string{"on-idle"}, cmdUnsetHook)
	if got := msg.CmdOutput; got != "Removed all hooks for on-idle\n" {
		t.Fatalf("unset-hook all output = %q", got)
	}

	msg = runOneShotCommand(t, sess, nil, cmdListHooks)
	if got := msg.CmdOutput; got != "No hooks registered.\n" {
		t.Fatalf("list-hooks empty output = %q", got)
	}
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
				outputSubCount:   len(sess.paneOutputSubs[pane2.ID]),
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
			sess.clients = []*clientConn{cc}
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
			sess.clients = []*clientConn{cc}
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
			sess.clients = []*clientConn{cc1, cc2}
			sess.sizeClient.Store(cc2)
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

		for i := 0; i < 2; i++ {
			_ = readCmdResultEvent(t, peerConn)
		}

		sess.paneOutputCallback()(pane.ID, []byte("hello"), 1)
		ev := readCmdResultEvent(t, peerConn)
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

	t.Run("throttle coalesces and sorts output events", func(t *testing.T) {
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

		for i := 0; i < 3; i++ {
			_ = readCmdResultEvent(t, peerConn)
		}

		sess.paneOutputCallback()(pane2.ID, []byte("pane2"), 1)
		sess.paneOutputCallback()(pane1.ID, []byte("pane1"), 1)
		sess.paneOutputCallback()(pane1.ID, []byte("pane1 again"), 2)

		first := readCmdResultEvent(t, peerConn)
		second := readCmdResultEvent(t, peerConn)
		if first.Type != EventOutput || second.Type != EventOutput {
			t.Fatalf("expected output events, got %+v and %+v", first, second)
		}
		if first.PaneID != pane1.ID || second.PaneID != pane2.ID {
			t.Fatalf("throttled pane order = [%d %d], want [%d %d]", first.PaneID, second.PaneID, pane1.ID, pane2.ID)
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
