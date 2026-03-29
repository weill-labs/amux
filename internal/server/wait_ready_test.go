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

func setupWaitReadyTestPane(t *testing.T, writeOverride func([]byte) (int, error)) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()
	return setupSendKeysWaitIdleTestPane(t, writeOverride)
}

func TestParseSendKeysArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantWait      bool
		wantInputIdle bool
		wantTimeout   time.Duration
		wantDelay     time.Duration
		wantHex       bool
		wantKeys      []string
		wantErr       string
	}{
		{
			name:        "wait idle and keys",
			args:        []string{"--wait", "idle", "--timeout", "25ms", "--delay-final", "150ms", "--hex", "6869", "Enter"},
			wantWait:    true,
			wantTimeout: 25 * time.Millisecond,
			wantDelay:   150 * time.Millisecond,
			wantHex:     true,
			wantKeys:    []string{"6869", "Enter"},
		},
		{
			name:          "wait input idle",
			args:          []string{"--wait", "ui=input-idle", "--timeout", "40ms", "task"},
			wantWait:      true,
			wantInputIdle: true,
			wantTimeout:   40 * time.Millisecond,
			wantKeys:      []string{"task"},
		},
		{
			name:        "literal args after first key",
			args:        []string{"task", "--wait", "ready"},
			wantTimeout: 10 * time.Second,
			wantKeys:    []string{"task", "--wait", "ready"},
		},
		{
			name:        "leading dash key stays literal",
			args:        []string{"-"},
			wantTimeout: 10 * time.Second,
			wantKeys:    []string{"-"},
		},
		{
			name:    "missing wait value",
			args:    []string{"--wait"},
			wantErr: "missing value for --wait",
		},
		{
			name:    "unsupported wait target ready",
			args:    []string{"--wait", "ready", "task"},
			wantErr: `send-keys: unsupported --wait target "ready" (want idle or ui=input-idle)`,
		},
		{
			name:    "unsupported wait target later",
			args:    []string{"--wait", "later", "task"},
			wantErr: `send-keys: unsupported --wait target "later" (want idle or ui=input-idle)`,
		},
		{
			name:    "missing timeout value",
			args:    []string{"--wait", "idle", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout value",
			args:    []string{"--wait", "idle", "--timeout", "later"},
			wantErr: "invalid timeout: later",
		},
		{
			name:    "removed continue flag",
			args:    []string{"--wait", "idle", "--continue-known-dialogs", "task"},
			wantErr: "unknown flag: --continue-known-dialogs",
		},
		{
			name:    "timeout requires wait target",
			args:    []string{"--timeout", "10ms", "task"},
			wantErr: "send-keys: --timeout requires --wait idle or --wait ui=input-idle",
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
			wantErr: "send-keys: --wait-ready was removed; use --wait idle",
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
			if tt.wantWait && got.waitTarget == sendKeysNoWait {
				t.Fatalf("wait target = %v, want a wait target", got.waitTarget)
			}
			if !tt.wantWait && got.waitTarget != sendKeysNoWait {
				t.Fatalf("wait target = %v, want no wait target", got.waitTarget)
			}
			if tt.wantInputIdle && got.waitTarget != sendKeysWaitInputIdle {
				t.Fatalf("wait target = %v, want input-idle", got.waitTarget)
			}
			if got.waitTimeout != tt.wantTimeout ||
				got.delayFinal != tt.wantDelay ||
				got.hexMode != tt.wantHex ||
				strings.Join(got.keys, "|") != strings.Join(tt.wantKeys, "|") {
				t.Fatalf("opts = %#v", got)
			}
		})
	}
}

func TestSendKeysWaitIdleUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1")
	if got := res.cmdErr; got != "usage: send-keys <pane> [--wait idle|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
	}
}

func TestCmdSendKeysWaitIdleWaitsForIdle(t *testing.T) {
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

	clientConn, _, done := startAsyncCommand(t, srv, sess, "send-keys", "pane-1", "--wait", "idle", "--timeout", "5s", "ab")
	clk.AwaitTimers(2)

	select {
	case got := <-writes:
		t.Fatalf("send-keys wrote before pane became idle: %q", got)
	default:
	}

	select {
	case <-done:
		t.Fatal("send-keys returned before pane became idle")
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
		t.Fatal("send-keys wait-idle command did not return")
	}
}

func TestSendKeysWaitIdleMissingPane(t *testing.T) {
	t.Parallel()

	srv, sess, _, cleanup := setupSendKeysWaitIdleTestPane(t, nil)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "missing", "--wait", "idle", "ship it")
	if !strings.Contains(res.cmdErr, "not found") {
		t.Fatalf("send-keys missing pane error = %q", res.cmdErr)
	}
}
