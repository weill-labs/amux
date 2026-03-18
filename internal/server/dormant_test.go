package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// TestAssertPaneLayoutConsistency_DormantPanesAllowed verifies that the
// invariant check does NOT flag dormant panes (they're intentionally
// outside the layout tree).
func TestAssertPaneLayoutConsistency_DormantPanesAllowed(t *testing.T) {
	t.Parallel()

	sess := newSession("test-dormant-ok")

	// Create a minimal window with a pane in the layout.
	pane1 := &mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}
	w := mux.NewWindow(pane1, 80, 24)
	w.ID = 1
	sess.Windows = append(sess.Windows, w)
	sess.Panes = append(sess.Panes, pane1)

	// Add a dormant pane (not in any layout tree).
	dormant := &mux.Pane{ID: 2, Meta: mux.PaneMeta{Name: "ssh-host", Dormant: true}}
	sess.Panes = append(sess.Panes, dormant)

	n := sess.assertPaneLayoutConsistency()
	if n != 0 {
		t.Errorf("dormant pane should not trigger consistency warning, got %d violations", n)
	}
}

// TestAssertPaneLayoutConsistency_OrphanDetected verifies that the invariant
// check flags non-dormant panes that are missing from the layout tree.
func TestAssertPaneLayoutConsistency_OrphanDetected(t *testing.T) {
	t.Parallel()

	sess := newSession("test-orphan-warn")

	// Create a minimal window with a pane in the layout.
	pane1 := &mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}
	w := mux.NewWindow(pane1, 80, 24)
	w.ID = 1
	sess.Windows = append(sess.Windows, w)
	sess.Panes = append(sess.Panes, pane1)

	// Add an orphaned pane (NOT dormant, NOT in layout tree).
	orphan := &mux.Pane{ID: 2, Meta: mux.PaneMeta{Name: "orphan-pane"}}
	sess.Panes = append(sess.Panes, orphan)

	n := sess.assertPaneLayoutConsistency()
	if n != 1 {
		t.Errorf("expected 1 consistency violation for orphaned pane, got %d", n)
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
