package server

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func newActorRecordingPane(sess *Session, id uint32, name string, sink *bytes.Buffer) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  name,
		Host:  mux.DefaultHost,
		Color: config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
	}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		_, _ = sink.Write(data)
		return len(data), nil
	})
}

func runTestCommandWithActor(t *testing.T, srv *Server, sess *Session, actorPaneID uint32, name string, args ...string) struct {
	output string
	cmdErr string
} {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	defer serverConn.Close()
	defer peerConn.Close()
	cc := newClientConn(serverConn)
	defer cc.Close()

	results := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		for {
			msg, err := ReadMsg(peerConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCmdResult {
				results <- struct {
					output string
					cmdErr string
				}{output: msg.CmdOutput, cmdErr: msg.CmdErr}
				return
			}
		}
	}()

	msg := &Message{
		Type:        MsgTypeCommand,
		CmdName:     name,
		CmdArgs:     args,
		ActorPaneID: actorPaneID,
	}

	go cc.handleCommand(srv, sess, msg)

	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s result", name)
		return struct {
			output string
			cmdErr string
		}{}
	}
}

func TestSendKeysPrefersActorWindowForDuplicatePaneNames(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	var activeSink bytes.Buffer
	var actorSink bytes.Buffer

	p1 := newActorRecordingPane(sess, 1, "shared", &activeSink)
	p2 := newTestPane(sess, 2, "active")
	p3 := newTestPane(sess, 3, "actor")
	p4 := newActorRecordingPane(sess, 4, "shared", &actorSink)

	w1 := newTestWindowWithPanes(t, sess, 1, "window-1", p1, p2)
	w1.FocusPane(p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "window-2", p3, p4)
	w2.FocusPane(p3)

	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = []*mux.Pane{p1, p2, p3, p4}

	res := runTestCommandWithActor(t, srv, sess, p3.ID, "send-keys", "shared", "echo ACTOR", "Enter")
	if res.cmdErr != "" {
		t.Fatalf("send-keys error: %s", res.cmdErr)
	}
	if activeSink.Len() != 0 {
		t.Fatalf("active window shared pane wrote %q, want none", activeSink.String())
	}
	if got := actorSink.String(); got != "echo ACTOR\r" {
		t.Fatalf("actor window shared pane writes = %q, want %q", got, "echo ACTOR\r")
	}
}

func TestZoomPrefersActorWindowForDuplicatePaneNames(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "shared")
	p2 := newTestPane(sess, 2, "active")
	p3 := newTestPane(sess, 3, "actor")
	p4 := newTestPane(sess, 4, "shared")

	w1 := newTestWindowWithPanes(t, sess, 1, "window-1", p1, p2)
	w1.FocusPane(p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "window-2", p3, p4)
	w2.FocusPane(p3)

	sess.Windows = []*mux.Window{w1, w2}
	sess.ActiveWindowID = w1.ID
	sess.Panes = []*mux.Pane{p1, p2, p3, p4}

	res := runTestCommandWithActor(t, srv, sess, p3.ID, "zoom", "shared")
	if res.cmdErr != "" {
		t.Fatalf("zoom error: %s", res.cmdErr)
	}
	if got := w1.ZoomedPaneID; got != 0 {
		t.Fatalf("active window zoomed pane = %d, want 0", got)
	}
	if got := w2.ZoomedPaneID; got != p4.ID {
		t.Fatalf("actor window zoomed pane = %d, want %d", got, p4.ID)
	}
}
