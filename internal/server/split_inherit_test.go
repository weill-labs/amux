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
	sess.mu.Lock()
	sess.Windows = append(sess.Windows, w)
	sess.ActiveWindowID = w.ID
	sess.mu.Unlock()

	// Create a proxy pane simulating a remote connection
	proxyID := sess.counter.Add(1)
	proxyPane := mux.NewProxyPane(proxyID, mux.PaneMeta{
		Name: "pane-2", Host: "gpu-server", Color: "f5e0dc",
	}, 40, 23,
		sess.paneOutputCallback(),
		sess.paneExitCallback(srv),
		func(data []byte) (int, error) { return len(data), nil },
	)
	sess.mu.Lock()
	sess.Panes = append(sess.Panes, proxyPane)
	w.Split(mux.SplitHorizontal, proxyPane)
	w.FocusPane(proxyPane) // make proxy pane active
	sess.mu.Unlock()

	// Send split command through handleCommand with a pipe to capture response.
	// Read responses asynchronously to avoid blocking.
	serverConn, clientConn := net.Pipe()
	cc := NewClientConn(serverConn)

	// Drain all messages from the server side
	type response struct {
		output string
		err    string
	}
	responses := make(chan response, 10)
	go func() {
		for {
			msg, err := ReadMsg(clientConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCmdResult {
				responses <- response{output: msg.CmdOutput, err: msg.CmdErr}
			}
			// Ignore layout broadcasts etc.
		}
	}()

	// Send the split command (no --host flag)
	go cc.handleCommand(srv, sess, &Message{
		Type:    MsgTypeCommand,
		CmdName: "split",
		CmdArgs: nil,
	})

	// Wait for the command result.
	// With the fix: the split tries to create a remote pane on gpu-server,
	// which fails because no RemoteManager is configured — but that proves
	// the host was inherited (the output mentions the remote host or the
	// error says "remote hosts").
	// Without the fix: a local pane is created ("new pane pane-3").
	select {
	case resp := <-responses:
		serverConn.Close()
		clientConn.Close()
		isRemote := strings.Contains(resp.output, "@gpu-server") ||
			strings.Contains(resp.err, "remote")
		if !isRemote {
			t.Errorf("split on proxy pane should inherit host (expect remote pane or remote error), got output=%q err=%q",
				strings.TrimSpace(resp.output), resp.err)
		}
	case <-time.After(5 * time.Second):
		serverConn.Close()
		clientConn.Close()
		t.Fatal("timeout waiting for split response")
	}
}
