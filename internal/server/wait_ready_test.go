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
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}
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
			wantErr: "invalid timeout: later",
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
				waitTimeout: 40 * time.Millisecond,
				keys:        []string{"task"},
			},
		},
		{
			name: "literal args after first key",
			args: []string{"task", "--wait", "ready"},
			want: sendKeysOptions{
				waitTimeout: 10 * time.Second,
				keys:        []string{"task", "--wait", "ready"},
			},
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

func TestSendKeysWaitReadyUsage(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "send-keys", "pane-1")
	if got := res.cmdErr; got != "usage: send-keys <pane> [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>..." {
		t.Fatalf("send-keys usage error = %q", got)
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
