package server

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestParseWaitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantAfter    uint64
		wantAfterSet bool
		wantTimeout  time.Duration
		wantErr      string
	}{
		{
			name:         "defaults",
			wantTimeout:  3 * time.Second,
			wantAfterSet: false,
		},
		{
			name:         "after and timeout",
			args:         []string{"--after", "7", "--timeout", "25ms"},
			wantAfter:    7,
			wantAfterSet: true,
			wantTimeout:  25 * time.Millisecond,
		},
		{
			name:    "missing after value",
			args:    []string{"--after"},
			wantErr: "missing value for --after",
		},
		{
			name:    "invalid after value",
			args:    []string{"--after", "bogus"},
			wantErr: "invalid generation: bogus",
		},
		{
			name:    "missing timeout value",
			args:    []string{"--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "unknown flag",
			args:    []string{"--wat"},
			wantErr: "unknown flag: --wat",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			afterGen, afterSet, timeout, err := parseWaitArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseWaitArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWaitArgs(%v): %v", tt.args, err)
			}
			if afterGen != tt.wantAfter || afterSet != tt.wantAfterSet || timeout != tt.wantTimeout {
				t.Fatalf("parsed = (%d, %t, %v), want (%d, %t, %v)", afterGen, afterSet, timeout, tt.wantAfter, tt.wantAfterSet, tt.wantTimeout)
			}
		})
	}
}

func TestParseTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		startIdx    int
		defaultTime time.Duration
		want        time.Duration
		wantErr     string
	}{
		{
			name:        "default timeout",
			args:        []string{"pane-1"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			want:        5 * time.Second,
		},
		{
			name:        "explicit timeout",
			args:        []string{"pane-1", "--timeout", "25ms"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			want:        25 * time.Millisecond,
		},
		{
			name:        "invalid timeout",
			args:        []string{"pane-1", "--timeout", "later"},
			startIdx:    1,
			defaultTime: 5 * time.Second,
			wantErr:     "invalid timeout: later",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseTimeout(tt.args, tt.startIdx, tt.defaultTime)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseTimeout(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTimeout(%v): %v", tt.args, err)
			}
			if got != tt.want {
				t.Fatalf("timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasAfterFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "missing", args: []string{"--timeout", "5s"}, want: false},
		{name: "present", args: []string{"--pane", "pane-1", "--after", "7"}, want: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasAfterFlag(tt.args); got != tt.want {
				t.Fatalf("hasAfterFlag(%v) = %t, want %t", tt.args, got, tt.want)
			}
		})
	}
}

func TestCmdCursorAndWaitUsageAndUnknownKind(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	cursorUsage := runTestCommand(t, srv, sess, "cursor")
	if got := cursorUsage.cmdErr; got != cursorCommandUsage {
		t.Fatalf("cursor usage error = %q", got)
	}

	cursorUnknown := runTestCommand(t, srv, sess, "cursor", "bogus")
	if got := cursorUnknown.cmdErr; got != "unknown cursor kind: bogus" {
		t.Fatalf("cursor unknown kind error = %q", got)
	}

	waitUsage := runTestCommand(t, srv, sess, "wait")
	if got := waitUsage.cmdErr; got != waitCommandUsage {
		t.Fatalf("wait usage error = %q", got)
	}

	waitUnknown := runTestCommand(t, srv, sess, "wait", "bogus")
	if got := waitUnknown.cmdErr; got != "unknown wait kind: bogus" {
		t.Fatalf("wait unknown kind error = %q", got)
	}
}

func TestCmdWaitSubcommandsUsageAndParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		fn      func(*CommandContext)
		wantErr string
	}{
		{
			name:    "content usage",
			fn:      cmdWaitFor,
			wantErr: "usage: wait content <pane> <substring> [--timeout <duration>]",
		},
		{
			name:    "idle usage",
			fn:      cmdWaitIdle,
			wantErr: "usage: wait idle <pane> [--timeout <duration>]",
		},
		{
			name:    "busy usage",
			fn:      cmdWaitBusy,
			wantErr: "usage: wait busy <pane> [--timeout <duration>]",
		},
		{
			name:    "ui parse error",
			fn:      cmdWaitUI,
			wantErr: "usage: wait ui <event> [--client <id>] [--after N] [--timeout <duration>]",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("test-" + strings.ReplaceAll(tt.name, " ", "-"))
			stopCrashCheckpointLoop(t, sess)
			defer stopSessionBackgroundLoops(t, sess)

			msg := runOneShotCommand(t, sess, tt.args, tt.fn)
			if got := msg.CmdErr; got != tt.wantErr {
				t.Fatalf("command error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestWaitForUIEventCurrentMatchWithoutAfterReturnsImmediately(t *testing.T) {
	t.Parallel()

	sess := newSession("test-wait-ui-current-no-after")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := newClientConn(nil)
	cc.ID = "client-1"
	cc.copyModeShown = true
	cc.uiGeneration = 3
	defer cc.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}
	})

	clientID, err := waitForUIEvent(sess, "", proto.UIEventCopyModeShown, 0, false, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForUIEvent current match: %v", err)
	}
	if clientID != "client-1" {
		t.Fatalf("client ID = %q, want %q", clientID, "client-1")
	}
}

func TestWaitForNextUIEventWaitsForFreshTransition(t *testing.T) {
	t.Parallel()

	sess := newSession("test-wait-ui-next-event")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := newClientConn(nil)
	cc.ID = "client-1"
	cc.inputIdle = true
	cc.uiGeneration = 2
	defer cc.Close()

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}
	})

	snapshot, err := sess.queryUIClient("client-1", proto.UIEventInputIdle)
	if err != nil {
		t.Fatalf("queryUIClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- waitForNextUIEvent(sess, snapshot, proto.UIEventInputIdle, 250*time.Millisecond)
	}()

	select {
	case err := <-done:
		t.Fatalf("waitForNextUIEvent returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	sess.enqueueUIEvent(cc, proto.UIEventInputBusy)
	sess.enqueueUIEvent(cc, proto.UIEventInputIdle)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForNextUIEvent error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForNextUIEvent did not return after fresh idle event")
	}
}

func TestCmdWaitTransitionCommandsDefaultToNextChange(t *testing.T) {
	t.Parallel()

	t.Run("layout", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()
		sess.generation.Store(4)

		peerConn, _, done := startAsyncCommand(t, srv, sess, "wait", "layout", "--timeout", "5s")

		waitUntil(t, func() bool {
			return mustSessionQuery(t, sess, func(sess *Session) bool {
				return sess.waiters.layoutWaiterRegistered(4)
			})
		})

		select {
		case <-done:
			t.Fatal("wait layout returned before next generation")
		case <-time.After(20 * time.Millisecond):
		}

		sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
			gen := s.generation.Add(1)
			s.notifyLayoutWaiters(gen)
			return commandMutationResult{}
		})

		msg := readMsgWithTimeout(t, peerConn)
		if got := msg.CmdOutput; got != "5\n" {
			t.Fatalf("wait layout output = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("wait layout command did not return")
		}
	})

	t.Run("clipboard", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()
		sess.waiters.setClipboardStateForTest(2, "old")

		peerConn, _, done := startAsyncCommand(t, srv, sess, "wait", "clipboard", "--timeout", "5s")

		waitUntil(t, func() bool {
			return mustSessionQuery(t, sess, func(sess *Session) bool {
				return sess.waiters.clipboardWaiterRegistered(2)
			})
		})

		select {
		case <-done:
			t.Fatal("wait clipboard returned before next update")
		case <-time.After(20 * time.Millisecond):
		}

		sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
			s.waiters.recordClipboard([]byte("new"))
			return commandMutationResult{}
		})

		msg := readMsgWithTimeout(t, peerConn)
		if got := msg.CmdOutput; got != "new\n" {
			t.Fatalf("wait clipboard output = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("wait clipboard command did not return")
		}
	})

	t.Run("hook", func(t *testing.T) {
		t.Parallel()

		srv, sess, cleanup := newCommandTestSession(t)
		defer cleanup()

		pane := newTestPane(sess, 1, "pane-1")
		w := newTestWindowWithPanes(t, sess, 1, "main", pane)
		sess.Windows = []*mux.Window{w}
		sess.ActiveWindowID = w.ID
		sess.Panes = []*mux.Pane{pane}
		sess.waiters.setHookStateForTest(7, nil)

		peerConn, _, done := startAsyncCommand(t, srv, sess, "wait", "hook", "on-idle", "--pane", "pane-1", "--timeout", "5s")

		waitUntil(t, func() bool {
			return mustSessionQuery(t, sess, func(sess *Session) bool {
				return sess.waiters.hookWaiterRegistered(7, "on-idle", 1, "pane-1")
			})
		})

		select {
		case <-done:
			t.Fatal("wait hook returned before next matching record")
		case <-time.After(20 * time.Millisecond):
		}

		sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
			s.waiters.appendHookResult(hookResultRecord{
				Event:    "on-idle",
				PaneID:   1,
				PaneName: "pane-1",
				Success:  true,
			})
			return commandMutationResult{}
		})

		msg := readMsgWithTimeout(t, peerConn)
		if got := msg.CmdOutput; got != "8 on-idle pane-1 success\n" {
			t.Fatalf("wait hook output = %q", got)
		}

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("wait hook command did not return")
		}
	})
}
