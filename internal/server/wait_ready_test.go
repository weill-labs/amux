package server

import (
	"strings"
	"testing"
	"time"

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
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), writeOverride)
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, pane)
	return srv, sess, pane, cleanup
}

func TestWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "wait", "ready")
	if got := res.cmdErr; got != "usage: wait ready <pane> [--timeout <duration>]" {
		t.Fatalf("wait-ready usage error = %q", got)
	}
}

func TestParseWaitReadyArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantPane string
		wantOpts waitReadyOptions
		wantErr  string
	}{
		{
			name:     "defaults",
			args:     []string{"pane-1"},
			wantPane: "pane-1",
			wantOpts: waitReadyOptions{timeout: 10 * time.Second},
		},
		{
			name:     "custom timeout",
			args:     []string{"pane-2", "--timeout", "25ms"},
			wantPane: "pane-2",
			wantOpts: waitReadyOptions{timeout: 25 * time.Millisecond},
		},
		{
			name:    "removed continue flag",
			args:    []string{"pane-1", "--continue-known-dialogs"},
			wantErr: waitReadyRemovedContinueFlagErr,
		},
		{
			name:    "missing timeout value",
			args:    []string{"pane-1", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout",
			args:    []string{"pane-1", "--timeout", "later"},
			wantErr: "invalid value for --timeout: later",
		},
		{
			name:    "unknown flag",
			args:    []string{"pane-1", "--bogus"},
			wantErr: "unknown flag: --bogus",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotPane, gotOpts, err := parseWaitReadyArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseWaitReadyArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWaitReadyArgs(%v) error = %v", tt.args, err)
			}
			if gotPane != tt.wantPane {
				t.Fatalf("pane = %q, want %q", gotPane, tt.wantPane)
			}
			if gotOpts != tt.wantOpts {
				t.Fatalf("opts = %#v, want %#v", gotOpts, tt.wantOpts)
			}
		})
	}
}

func TestParseSendKeysArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    sendKeysOptions
		wantErr string
	}{
		{
			name: "wait ready and keys",
			args: []string{"--wait", "ready", "--timeout", "25ms", "--delay-final", "150ms", "--hex", "6869", "Enter"},
			want: sendKeysOptions{
				waitTarget:  sendKeysWaitReady,
				transport:   sendKeysViaPTY,
				waitTimeout: 25 * time.Millisecond,
				delayFinal:  150 * time.Millisecond,
				hexMode:     true,
				keys:        []string{"6869", "Enter"},
			},
		},
		{
			name: "wait input idle",
			args: []string{"--wait", "ui=input-idle", "--timeout", "40ms", "task"},
			want: sendKeysOptions{
				waitTarget:  sendKeysWaitInputIdle,
				transport:   sendKeysViaClient,
				waitTimeout: 40 * time.Millisecond,
				keys:        []string{"task"},
			},
		},
		{
			name: "explicit via client",
			args: []string{"--via", "client", "task"},
			want: sendKeysOptions{
				transport:         sendKeysViaClient,
				transportExplicit: true,
				waitTimeout:       10 * time.Second,
				keys:              []string{"task"},
			},
		},
		{
			name: "explicit client selection",
			args: []string{"--via", "client", "--client", "client-2", "task"},
			want: sendKeysOptions{
				transport:         sendKeysViaClient,
				transportExplicit: true,
				requestedClientID: "client-2",
				waitTimeout:       10 * time.Second,
				keys:              []string{"task"},
			},
		},
		{
			name: "literal args after first key",
			args: []string{"task", "--wait", "ready"},
			want: sendKeysOptions{
				transport:   sendKeysViaPTY,
				waitTimeout: 10 * time.Second,
				keys:        []string{"task", "--wait", "ready"},
			},
		},
		{
			name:    "wait input idle rejects via pty",
			args:    []string{"--via", "pty", "--wait", "ui=input-idle", "task"},
			wantErr: "send-keys: --wait ui=input-idle requires --via client",
		},
		{
			name:    "unsupported via target",
			args:    []string{"--via", "ssh", "task"},
			wantErr: `send-keys: unsupported --via target "ssh" (want pty or client)`,
		},
		{
			name:    "missing wait value",
			args:    []string{"--wait"},
			wantErr: "missing value for --wait",
		},
		{
			name:    "unsupported wait target",
			args:    []string{"--wait", "later", "task"},
			wantErr: `send-keys: unsupported --wait target "later" (want ready or ui=input-idle)`,
		},
		{
			name:    "missing timeout value",
			args:    []string{"--wait", "ready", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--wait", "ready", "--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "removed continue flag",
			args:    []string{"--wait", "ready", "--continue-known-dialogs", "task"},
			wantErr: sendKeysRemovedContinueFlagErr,
		},
		{
			name:    "timeout requires wait target",
			args:    []string{"--timeout", "10ms", "task"},
			wantErr: "send-keys: --timeout requires --wait ready or --wait ui=input-idle",
		},
		{
			name:    "missing delay-final value",
			args:    []string{"--delay-final"},
			wantErr: "missing value for --delay-final",
		},
		{
			name:    "invalid delay-final value",
			args:    []string{"--delay-final", "later", "task"},
			wantErr: "invalid delay-final: later",
		},
		{
			name:    "legacy flag rejected",
			args:    []string{"--wait-ready", "task"},
			wantErr: "send-keys: --wait-ready was removed; use --wait ready",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSendKeysArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseSendKeysArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSendKeysArgs(%v) error = %v", tt.args, err)
			}
			if got.waitTarget != tt.want.waitTarget ||
				got.transport != tt.want.transport ||
				got.transportExplicit != tt.want.transportExplicit ||
				got.requestedClientID != tt.want.requestedClientID ||
				got.waitTimeout != tt.want.waitTimeout ||
				got.delayFinal != tt.want.delayFinal ||
				got.hexMode != tt.want.hexMode ||
				strings.Join(got.keys, "|") != strings.Join(tt.want.keys, "|") {
				t.Fatalf("opts = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWaitReadyReturnsWhenPaneIsIdleAndVTIdle(t *testing.T) {
	t.Parallel()

	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	pane.SetCreatedAt(time.Now().Add(-3 * time.Second))

	res := runTestCommand(t, srv, sess, "wait", "ready", "pane-1", "--timeout", "50ms")
	if res.cmdErr != "" || strings.TrimSpace(res.output) != "ready" {
		t.Fatalf("wait-ready result = %#v", res)
	}
}

func TestWaitForPaneReadyReturnsSessionShuttingDown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	err := waitForPaneReady(sess, "pane-1", resolvedPaneRef{}, waitReadyOptions{timeout: time.Millisecond})
	if err == nil || err.Error() != "session shutting down" {
		t.Fatalf("waitForPaneReady session shutdown error = %v, want session shutting down", err)
	}
}

func TestWaitReadyFailsWhenPaneDisappearsMidWait(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()
	defer func() {
		_ = pane.Close()
		_ = pane.WaitClosed()
	}()

	sess.Clock = clk
	sess.ensureIdleTracker().VTIdleSettle = 100 * time.Millisecond
	pane.SetCreatedAt(clk.Now())

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait", "ready", "pane-1", "--timeout", "5s")
	clk.AwaitTimers(3)

	sess.enqueueCommandMutation(func(s *MutationContext) commandMutationResult {
		s.finalizePaneRemoval(pane.ID)
		return commandMutationResult{}
	})

	clk.Advance(110 * time.Millisecond)

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdErr; got != `pane "pane-1" disappeared while waiting to become ready` {
		t.Fatalf("wait-ready error = %q, want pane disappearance error", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-ready pane disappearance command did not return")
	}
}

func TestWaitReadyRestartsSettleTimerAfterExpiredWindowSeesNewOutput(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	sess.Clock = clk
	sess.ensureIdleTracker().VTIdleSettle = 100 * time.Millisecond
	pane.SetCreatedAt(clk.Now())

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait", "ready", "pane-1", "--timeout", "5s")
	clk.AwaitTimers(3)

	mutationStarted := make(chan struct{})
	mutationRelease := make(chan struct{})
	mutationDone := make(chan struct{})
	go func() {
		defer close(mutationDone)
		sess.enqueueCommandMutation(func(s *MutationContext) commandMutationResult {
			close(mutationStarted)
			<-mutationRelease
			return commandMutationResult{}
		})
	}()
	<-mutationStarted

	// Hold the session query so the old settle tick can fire, then inject a
	// fresh VT output sample before syncReady re-reads state.
	clk.Advance(100 * time.Millisecond)
	sess.ensureIdleTracker().TrackOutput(pane.ID, func() {}, func(time.Time) {})
	close(mutationRelease)

	select {
	case <-mutationDone:
	case <-time.After(time.Second):
		t.Fatal("blocking mutation did not release")
	}

	// Initial wait-start work creates 3 timer ops. The replacement output adds
	// vt-idle and input-idle tracker timers (+2), and syncReady re-arms the
	// command settle timer (+1).
	clk.AwaitTimers(6)

	select {
	case <-done:
		t.Fatal("wait-ready returned before the replacement settle window elapsed")
	case <-time.After(20 * time.Millisecond):
	}

	clk.Advance(110 * time.Millisecond)

	msg := readMsgWithTimeout(t, clientConn)
	if got := strings.TrimSpace(msg.CmdOutput); got != "ready" {
		t.Fatalf("wait-ready output = %q, want ready", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-ready command did not return after replacement settle window elapsed")
	}
}

func TestSendKeysWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1")
	if got := res.cmdErr; got != "usage: send-keys <pane> [--via pty|client] [--client <id>] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestCmdSendKeysWaitReadyWaitsForReady(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	writes := make(chan string, 1)
	srv, sess, pane, cleanup := setupWaitReadyTestPane(t, func(data []byte) (int, error) {
		writes <- string(data)
		return len(data), nil
	})
	defer cleanup()

	sess.Clock = clk
	sess.ensureIdleTracker().VTIdleSettle = 100 * time.Millisecond
	pane.SetCreatedAt(clk.Now())

	clientConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--wait", "ready", "--timeout", "5s", "ab")
	clk.AwaitTimers(3)

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

func TestSendKeysWaitReadyMissingPane(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupWaitReadyTestPane(t, nil)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "missing", "--wait", "ready", "ship it")
	if !strings.Contains(res.cmdErr, "not found") {
		t.Fatalf("send-keys missing pane error = %q", res.cmdErr)
	}
}
