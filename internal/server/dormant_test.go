package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// TestAssertPaneLayoutConsistency verifies that the invariant check skips
// dormant panes (intentionally outside the layout tree) and flags non-dormant
// orphans.
func TestAssertPaneLayoutConsistency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dormant        bool
		wantViolations int
	}{
		{"dormant pane allowed", true, 0},
		{"orphan detected", false, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("test-consistency")
			stopCrashCheckpointLoop(t, sess)

			pane1 := mux.NewProxyPane(1, mux.PaneMeta{
				Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc",
			}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
			w := mux.NewWindow(pane1, 80, 24)
			w.ID = 1
			sess.Windows = append(sess.Windows, w)
			sess.Panes = append(sess.Panes, pane1)

			extra := mux.NewProxyPane(2, mux.PaneMeta{
				Name: "extra-pane", Host: mux.DefaultHost, Color: "f2cdcd", Dormant: tt.dormant,
			}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
			sess.Panes = append(sess.Panes, extra)

			if n := sess.assertPaneLayoutConsistency(); n != tt.wantViolations {
				t.Errorf("got %d violations, want %d", n, tt.wantViolations)
			}
		})
	}
}

// TestCmdListShowsDormant verifies that the list command shows "(dormant)"
// in the WINDOW column for dormant panes.
func TestCmdListShowsDormant(t *testing.T) {
	t.Parallel()

	srv, err := NewServer("test-list-dormant")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	sess := srv.sessions["test-list-dormant"]

	// Create pane-1 in a window.
	pane1, err := sess.createPane(srv, 80, 23)
	if err != nil {
		t.Fatalf("createPane: %v", err)
	}
	pane1.Start()
	w := mux.NewWindow(pane1, 80, 24)
	w.ID = sess.windowCounter.Add(1)
	w.Name = "window-1"
	sess.mu.Lock()
	sess.Windows = append(sess.Windows, w)
	sess.ActiveWindowID = w.ID

	// Add a dormant pane.
	dormantID := sess.counter.Add(1)
	dormantPane := mux.NewProxyPane(dormantID, mux.PaneMeta{
		Name: "ssh-conn", Dormant: true, Color: "f5e0dc",
	}, 80, 23,
		sess.paneOutputCallback(),
		sess.paneExitCallback(srv),
		func(data []byte) (int, error) { return len(data), nil },
	)
	sess.Panes = append(sess.Panes, dormantPane)
	sess.mu.Unlock()

	// Run the list command via net.Pipe.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	cc := NewClientConn(serverConn)

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
		CmdName: "list",
	})

	select {
	case r := <-results:
		if r.cmdErr != "" {
			t.Fatalf("list failed: %s", r.cmdErr)
		}
		if !strings.Contains(r.output, "(dormant)") {
			t.Errorf("list should show (dormant) for dormant pane, got:\n%s", r.output)
		}
		if !strings.Contains(r.output, "window-1") {
			t.Errorf("list should show window-1 for non-dormant pane, got:\n%s", r.output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for list response")
	}
}
