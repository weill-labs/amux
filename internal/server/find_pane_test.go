package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestSessionFindPaneByRef(t *testing.T) {
	t.Parallel()

	sess := newSession("test-find")
	stopCrashCheckpointLoop(t, sess)

	// Create mock panes in the flat registry (not in any window layout)
	panes := []struct {
		id   uint32
		name string
	}{
		{1, "pane-1"},
		{2, "pane-2"},
		{10, "agent-task"},
	}
	for _, p := range panes {
		sess.Panes = append(sess.Panes, newProxyPane(
			p.id,
			mux.PaneMeta{Name: p.name, Host: mux.DefaultHost, Color: "f5e0dc"},
			80,
			23,
			nil,
			nil,
			func(data []byte) (int, error) { return len(data), nil },
		))
	}

	tests := []struct {
		name   string
		ref    string
		wantID uint32
	}{
		{"exact name", "pane-1", 1},
		{"exact name 2", "agent-task", 10},
		{"numeric ID", "2", 2},
		{"numeric ID 10", "10", 10},
		{"prefix match", "pane-", 1}, // first prefix match
		{"prefix match agent", "agent", 10},
		{"no match", "nonexistent", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sess.findPaneByRef(tt.ref)
			if tt.wantID == 0 {
				if got != nil {
					t.Errorf("findPaneByRef(%q) = pane %d, want nil", tt.ref, got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("findPaneByRef(%q) = nil, want pane %d", tt.ref, tt.wantID)
			}
			if got.ID != tt.wantID {
				t.Errorf("findPaneByRef(%q) = pane %d, want pane %d", tt.ref, got.ID, tt.wantID)
			}
		})
	}
}

// TestKillOrphanedPaneViaFallback exercises the full command path for killing
// a pane that exists in Session.Panes but not in any window's layout tree.
// This is the exact scenario that causes ghost panes (LAB-210).
func TestKillOrphanedPaneViaFallback(t *testing.T) {
	t.Parallel()

	srv, err := NewServer("test-kill-orphan")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	sess := srv.sessions["test-kill-orphan"]

	// Create pane-1 in a window (the normal, non-orphaned pane).
	pane1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	pane1.Start()
	w := mux.NewWindow(pane1, 80, 24)
	w.ID = sess.windowCounter.Add(1)
	w.Name = "window-1"
	sess.Windows = append(sess.Windows, w)
	sess.ActiveWindowID = w.ID

	// Create an orphaned pane: add to flat registry but NOT to any window layout.
	// This simulates a dormant SSH takeover pane or a pane orphaned by a race.
	orphanID := sess.counter.Add(1)
	orphanPane := newProxyPane(orphanID, mux.PaneMeta{
		Name: "orphan-pane", Host: "remote", Color: "f5e0dc",
	}, 80, 23,
		sess.paneOutputCallback(),
		sess.paneExitCallback(),
		func(data []byte) (int, error) { return len(data), nil },
	)
	sess.Panes = append(sess.Panes, orphanPane)

	// Verify the orphan is in the flat registry but not in any layout tree.
	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		inWindow bool
		hasPane  bool
	} {
		return struct {
			inWindow bool
			hasPane  bool
		}{
			inWindow: sess.FindWindowByPaneID(orphanID) != nil,
			hasPane:  sess.hasPane(orphanID),
		}
	})
	if state.inWindow {
		t.Fatal("orphan pane should NOT be in any window layout")
	}
	if !state.hasPane {
		t.Fatal("orphan pane should be in Session.Panes")
	}
	assertSessionLayoutConsistent(t, sess, orphanID)

	// Send "kill orphan-pane" through the command path via net.Pipe.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	cc := NewClientConn(serverConn)
	defer cc.Close()

	type cmdResult struct {
		output, cmdErr string
	}
	results := make(chan cmdResult, 1)
	go func() {
		for {
			msg, err := ReadMsg(clientConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCmdResult {
				results <- cmdResult{output: msg.CmdOutput, cmdErr: msg.CmdErr}
				return
			}
		}
	}()

	go cc.handleCommand(srv, sess, &Message{
		Type:    MsgTypeCommand,
		CmdName: "kill",
		CmdArgs: []string{"orphan-pane"},
	})

	select {
	case r := <-results:
		if r.cmdErr != "" {
			t.Fatalf("kill orphan-pane should succeed, got error: %s", r.cmdErr)
		}
		if !strings.Contains(r.output, "Killed orphan-pane") {
			t.Errorf("expected 'Killed orphan-pane', got: %s", r.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for kill response")
	}

	// Verify the orphan is gone from the flat registry.
	stillExists := mustSessionQuery(t, sess, func(sess *Session) bool {
		return sess.hasPane(orphanID)
	})
	if stillExists {
		t.Error("orphan pane should be removed from Session.Panes after kill")
	}
	assertSessionLayoutConsistent(t, sess)
}
