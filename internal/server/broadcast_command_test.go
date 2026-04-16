package server

import (
	"reflect"
	"strings"
	"testing"

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
			name:      "no args",
			wantError: "usage: broadcast",
		},
		{
			name:      "missing selector",
			args:      []string{"echo hello", "Enter"},
			wantError: "usage: broadcast",
		},
		{
			name:      "missing panes value",
			args:      []string{"--panes"},
			wantError: "usage: broadcast",
		},
		{
			name:      "missing window value",
			args:      []string{"--window"},
			wantError: "usage: broadcast",
		},
		{
			name:      "missing match value",
			args:      []string{"--match"},
			wantError: "usage: broadcast",
		},
		{
			name:      "missing keys after selector",
			args:      []string{"--panes", "pane-1"},
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

func TestResolveBroadcastTargetsForActorRejectsMissingSelector(t *testing.T) {
	t.Parallel()

	sess := newSession("test-broadcast-missing-selector")
	stopCrashCheckpointLoop(t, sess)

	targets, err := resolveBroadcastTargetsForActor(sess, 0, broadcastCommandArgs{})
	if err == nil || err.Error() != broadcastUsage {
		t.Fatalf("resolveBroadcastTargetsForActor() error = %v, want %q", err, broadcastUsage)
	}
	if targets != nil {
		t.Fatalf("resolveBroadcastTargetsForActor() targets = %v, want nil", targets)
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
