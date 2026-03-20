package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// TestSplitInheritsRemoteHost verifies that the split command inherits the
// active proxy pane's host. No SSH required — uses a mock proxy pane and
// exercises the full handleCommand path.
func TestSplitInheritsRemoteHost(t *testing.T) {
	t.Parallel()

	srv, err := NewServer("test-inherit")
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Shutdown()

	sess := srv.sessions["test-inherit"]

	// Create pane-1 + window
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

	// Create a proxy pane simulating a remote connection
	proxyID := sess.counter.Add(1)
	proxyPane := mux.NewProxyPane(proxyID, mux.PaneMeta{
		Name: "pane-2", Host: "gpu-server", Color: "f5e0dc",
	}, 40, 23,
		sess.paneOutputCallback(),
		sess.paneExitCallback(),
		func(data []byte) (int, error) { return len(data), nil },
	)
	sess.Panes = append(sess.Panes, proxyPane)
	w.Split(mux.SplitHorizontal, proxyPane)
	w.FocusPane(proxyPane) // make proxy pane active

	// Send the split command through handleCommand with a pipe to capture the response.
	// handleCommand may send layout broadcasts before the cmd result, so drain
	// messages asynchronously and pick out the MsgTypeCmdResult.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	cc := NewClientConn(serverConn)

	type cmdResult struct {
		output, err string
	}
	results := make(chan cmdResult, 1)
	go func() {
		for {
			msg, err := ReadMsg(clientConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCmdResult {
				results <- cmdResult{output: msg.CmdOutput, err: msg.CmdErr}
				return
			}
		}
	}()

	go cc.handleCommand(srv, sess, &Message{
		Type:    MsgTypeCommand,
		CmdName: "split",
	})

	// With the fix: the split tries to create a remote pane on gpu-server,
	// which fails because no RemoteManager is configured — but that proves
	// the host was inherited (the response mentions the remote host).
	// Without the fix: a local pane is created ("new pane pane-3").
	select {
	case r := <-results:
		isRemote := strings.Contains(r.output, "@gpu-server") ||
			strings.Contains(r.err, "remote")
		if !isRemote {
			t.Errorf("split on proxy pane should inherit host (expect remote pane or remote error), got output=%q err=%q",
				strings.TrimSpace(r.output), r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for split response")
	}
}
