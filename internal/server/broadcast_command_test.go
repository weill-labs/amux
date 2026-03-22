package server

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestParseBroadcastCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		want      broadcastCommandArgs
		wantError string
	}{
		{
			name: "panes selector with hex",
			args: []string{"--panes", "pane-1,pane-2", "--hex", "61", "0d"},
			want: broadcastCommandArgs{
				paneRefs: []string{"pane-1", "pane-2"},
				hexMode:  true,
				keys:     []string{"61", "0d"},
			},
		},
		{
			name: "window selector",
			args: []string{"--window", "logs", "echo hello", "Enter"},
			want: broadcastCommandArgs{
				windowRef: "logs",
				keys:      []string{"echo hello", "Enter"},
			},
		},
		{
			name: "match selector",
			args: []string{"--match", "worker-*", "echo hello", "Enter"},
			want: broadcastCommandArgs{
				matchPattern: "worker-*",
				keys:         []string{"echo hello", "Enter"},
			},
		},
		{
			name: "double dash starts key args",
			args: []string{"--panes", "pane-1", "--", "--window", "Enter"},
			want: broadcastCommandArgs{
				paneRefs: []string{"pane-1"},
				keys:     []string{"--window", "Enter"},
			},
		},
		{
			name:      "missing selector",
			args:      []string{"echo hello", "Enter"},
			wantError: "usage: broadcast",
		},
		{
			name:      "multiple selectors",
			args:      []string{"--panes", "pane-1", "--window", "1", "echo hello", "Enter"},
			wantError: "specify exactly one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseBroadcastCommandArgs(tt.args)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("parseBroadcastCommandArgs() error = %v, want substring %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBroadcastCommandArgs() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseBroadcastCommandArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveBroadcastTargets(t *testing.T) {
	t.Parallel()

	sess := newSession("test-broadcast-targets")
	stopCrashCheckpointLoop(t, sess)

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "worker-alpha")
	p3 := newTestPane(sess, 3, "worker-beta")

	w1 := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "logs", p3)
	sess.Panes = []*mux.Pane{p1, p2, p3}
	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID

	tests := []struct {
		name      string
		args      broadcastCommandArgs
		wantNames []string
		wantError string
	}{
		{
			name:      "pane refs preserve order and dedupe",
			args:      broadcastCommandArgs{paneRefs: []string{"worker-alpha", "pane-1", "worker-alpha"}},
			wantNames: []string{"worker-alpha", "pane-1"},
		},
		{
			name:      "window selector",
			args:      broadcastCommandArgs{windowRef: "logs"},
			wantNames: []string{"worker-beta"},
		},
		{
			name:      "match selector",
			args:      broadcastCommandArgs{matchPattern: "worker-*"},
			wantNames: []string{"worker-alpha", "worker-beta"},
		},
		{
			name:      "empty match",
			args:      broadcastCommandArgs{matchPattern: "missing-*"},
			wantError: `broadcast: no panes match "missing-*"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			targets, err := resolveBroadcastTargets(sess, tt.args)
			if tt.wantError != "" {
				if err == nil || err.Error() != tt.wantError {
					t.Fatalf("resolveBroadcastTargets() error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBroadcastTargets() error = %v", err)
			}

			names := make([]string, 0, len(targets))
			for _, target := range targets {
				names = append(names, target.paneName)
			}
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Fatalf("resolveBroadcastTargets() names = %v, want %v", names, tt.wantNames)
			}
		})
	}
}

func TestCmdBroadcastSendsKeysAndDedupes(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, writes1 := newBroadcastTestPane(sess, 1, "pane-1", nil)
	p2, writes2 := newBroadcastTestPane(sess, 2, "pane-2", nil)
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	res := runTestCommand(t, srv, sess, "broadcast", "--panes", "pane-1,pane-1,pane-2", "hello", "Enter")
	if res.cmdErr != "" {
		t.Fatalf("broadcast error = %s", res.cmdErr)
	}
	if strings.TrimSpace(res.output) != "Sent 6 bytes to 2 panes: pane-1, pane-2" {
		t.Fatalf("broadcast output = %q", res.output)
	}
	if got := writes1.String(); got != "hello\r" {
		t.Fatalf("pane-1 writes = %q, want %q", got, "hello\r")
	}
	if got := writes2.String(); got != "hello\r" {
		t.Fatalf("pane-2 writes = %q, want %q", got, "hello\r")
	}
}

func TestCmdBroadcastReportsWriteFailures(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1, writes1 := newBroadcastTestPane(sess, 1, "pane-1", nil)
	p2, _ := newBroadcastTestPane(sess, 2, "pane-2", errors.New("boom"))
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	sess.Windows = []*mux.Window{w}
	sess.ActiveWindowID = w.ID
	sess.Panes = []*mux.Pane{p1, p2}

	res := runTestCommand(t, srv, sess, "broadcast", "--panes", "pane-1,pane-2", "ping")
	if !strings.Contains(res.cmdErr, "broadcast: failed for 1/2 panes: pane-2: boom") {
		t.Fatalf("broadcast error = %q", res.cmdErr)
	}
	if got := writes1.String(); got != "ping" {
		t.Fatalf("pane-1 writes = %q, want %q", got, "ping")
	}
}

func TestCmdBroadcastUsageAndResolutionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   []string
		errSub string
	}{
		{
			name:   "usage",
			args:   nil,
			errSub: "usage: broadcast",
		},
		{
			name:   "invalid hex",
			args:   []string{"--panes", "pane-1", "--hex", "zz"},
			errSub: "invalid hex: zz",
		},
		{
			name:   "missing window",
			args:   []string{"--window", "missing", "ping"},
			errSub: `window "missing" not found`,
		},
		{
			name:   "invalid glob",
			args:   []string{"--match", "[", "ping"},
			errSub: `invalid match pattern "["`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			p1, _ := newBroadcastTestPane(sess, 1, "pane-1", nil)
			w := newTestWindowWithPanes(t, sess, 1, "main", p1)
			sess.Windows = []*mux.Window{w}
			sess.ActiveWindowID = w.ID
			sess.Panes = []*mux.Pane{p1}

			res := runTestCommand(t, srv, sess, "broadcast", tt.args...)
			if !strings.Contains(res.cmdErr, tt.errSub) {
				t.Fatalf("broadcast error = %q, want substring %q", res.cmdErr, tt.errSub)
			}
		})
	}
}

func newBroadcastTestPane(sess *Session, id uint32, name string, writeErr error) (*mux.Pane, *strings.Builder) {
	var mu sync.Mutex
	writes := &strings.Builder{}
	pane := newProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		if writeErr != nil {
			return 0, writeErr
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = writes.Write(data)
		return len(data), nil
	})
	return pane, writes
}
