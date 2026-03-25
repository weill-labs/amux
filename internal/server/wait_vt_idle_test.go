package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func startAsyncCommand(t *testing.T, srv *Server, sess *Session, name string, args ...string) (net.Conn, *clientConn, <-chan struct{}) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	cc := newClientConn(serverConn)
	done := make(chan struct{})
	go func() {
		defer close(done)
		cc.handleCommand(srv, sess, &Message{
			Type:    MsgTypeCommand,
			CmdName: name,
			CmdArgs: args,
		})
	}()

	t.Cleanup(func() {
		cc.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	})

	return clientConn, cc, done
}

func setupWaitVTIdleTestPane(t *testing.T) (*Server, *Session, *mux.Pane, func()) {
	t.Helper()

	srv, sess, cleanup := newCommandTestSession(t)
	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	})
	w := newTestWindowWithPanes(t, sess, 1, "main", pane)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{pane}
	return srv, sess, pane, cleanup
}

func TestCmdWaitVTIdleUsage(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	msg := runOneShotCommand(t, sess, nil, cmdWaitVTIdle)
	if got := msg.CmdErr; got != "usage: wait vt-idle <pane> [--settle <duration>] [--timeout <duration>]" {
		t.Fatalf("wait-vt-idle usage error = %q", got)
	}
}

func TestParseWaitVTIdleArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantPane string
		wantOpts waitVTIdleOptions
		wantErr  string
	}{
		{
			name:     "defaults",
			args:     []string{"pane-1"},
			wantPane: "pane-1",
			wantOpts: waitVTIdleOptions{settle: DefaultVTIdleSettle, timeout: DefaultVTIdleTimeout},
		},
		{
			name:     "custom settle and timeout",
			args:     []string{"pane-2", "--settle", "25ms", "--timeout", "3s"},
			wantPane: "pane-2",
			wantOpts: waitVTIdleOptions{settle: 25 * time.Millisecond, timeout: 3 * time.Second},
		},
		{
			name:    "missing settle value",
			args:    []string{"pane-1", "--settle"},
			wantErr: "missing value for --settle",
		},
		{
			name:    "invalid settle",
			args:    []string{"pane-1", "--settle", "later"},
			wantErr: "invalid settle: later",
		},
		{
			name:    "missing timeout value",
			args:    []string{"pane-1", "--timeout"},
			wantErr: "missing value for --timeout",
		},
		{
			name:    "invalid timeout",
			args:    []string{"pane-1", "--timeout", "soon"},
			wantErr: "invalid timeout: soon",
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

			gotPane, gotOpts, err := parseWaitVTIdleArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("parseWaitVTIdleArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWaitVTIdleArgs(%v) error = %v", tt.args, err)
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

func TestCmdWaitVTIdleImmediateWhenAlreadySettled(t *testing.T) {
	t.Parallel()

	_, sess, pane, cleanup := setupWaitVTIdleTestPane(t)
	defer cleanup()

	pane.SetCreatedAt(time.Now().Add(-time.Second))

	msg := runOneShotCommand(t, sess, []string{"pane-1", "--settle", "20ms", "--timeout", "100ms"}, cmdWaitVTIdle)
	if got := strings.TrimSpace(msg.CmdOutput); got != "vt-idle" {
		t.Fatalf("wait-vt-idle output = %q, want vt-idle", got)
	}
}

func TestCmdWaitVTIdleTimeout(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	srv, sess, _, cleanup := setupWaitVTIdleTestPane(t)
	defer cleanup()
	sess.Clock = clk
	sess.vtIdle = NewVTIdleTracker(clk)

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait", "vt-idle", "pane-1", "--settle", "200ms", "--timeout", "40ms")

	// Wait for cmdWaitVTIdle to create its two timers (settle + timeout).
	// Because fakeTimer.ch is buffered, Advance can fire a timer even if the
	// goroutine hasn't entered its select yet.
	clk.AwaitTimers(2)

	// Advance past the timeout deadline — fires into the buffered channel.
	clk.Advance(50 * time.Millisecond)

	msg := readMsgWithTimeout(t, clientConn)
	if got := msg.CmdErr; got != "timeout waiting for pane-1 to become vt-idle" {
		t.Fatalf("wait-vt-idle timeout error = %q", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-vt-idle timeout command did not return")
	}
}

func TestCmdWaitVTIdleResetsSettleTimerOnOutput(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	srv, sess, pane, cleanup := setupWaitVTIdleTestPane(t)
	defer cleanup()
	sess.Clock = clk
	sess.vtIdle = NewVTIdleTracker(clk)

	clientConn, _, done := startAsyncCommand(t, srv, sess, "wait", "vt-idle", "pane-1", "--settle", "100ms", "--timeout", "5s")

	// Wait for the two initial timers (settle + timeout).
	clk.AwaitTimers(2)

	// Send output — the event loop calls TrackOutput (AfterFunc, +1) and
	// notifies the command handler which calls resetTimer (Reset, +1).
	pane.FeedOutput([]byte("first"))
	clk.AwaitTimers(4) // 2 initial + 1 AfterFunc + 1 Reset
	clk.Advance(50 * time.Millisecond)

	// More output — same pattern: AfterFunc (+1) + Reset (+1).
	pane.FeedOutput([]byte("second"))
	clk.AwaitTimers(6) // 4 prev + 1 AfterFunc + 1 Reset
	clk.Advance(50 * time.Millisecond)

	// Advance past the settle window from the last output.
	clk.Advance(110 * time.Millisecond)

	msg := readMsgWithTimeout(t, clientConn)
	if got := strings.TrimSpace(msg.CmdOutput); got != "vt-idle" {
		t.Fatalf("wait-vt-idle output = %q, want vt-idle", got)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait-vt-idle command did not return after settling")
	}
}

func TestPaneOutputCallbackEmitsVTIdleEventAfterQuiescence(t *testing.T) {
	t.Parallel()

	sess := newSession("test-vt-idle-event")
	sess.VTIdleSettle = 20 * time.Millisecond
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, sess.paneOutputCallback(), nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	sess.Panes = []*mux.Pane{pane}

	res := sess.enqueueEventSubscribe(eventFilter{Types: []string{EventVTIdle}}, false)
	defer sess.enqueueEventUnsubscribe(res.sub)

	pane.FeedOutput([]byte("hello"))

	select {
	case data := <-res.sub.ch:
		var ev Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if ev.Type != EventVTIdle || ev.PaneID != pane.ID || ev.PaneName != "pane-1" || ev.Host != mux.DefaultHost {
			t.Fatalf("unexpected vt-idle event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("vt-idle event was not emitted")
	}
}

func TestCurrentStateEventsIncludeVTIdleForSettledPane(t *testing.T) {
	t.Parallel()

	sess := newSession("test-vt-idle-snapshot")
	sess.VTIdleSettle = 20 * time.Millisecond
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane := newProxyPane(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	pane.SetCreatedAt(time.Now().Add(-time.Second))
	sess.Panes = []*mux.Pane{pane}

	events := sess.currentStateEvents()
	for _, ev := range events {
		if ev.Type == EventVTIdle && ev.PaneID == pane.ID && ev.PaneName == "pane-1" {
			return
		}
	}

	t.Fatalf("currentStateEvents did not include vt-idle for pane %d", pane.ID)
}
