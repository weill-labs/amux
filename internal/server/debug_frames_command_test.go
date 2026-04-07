package server

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestQueuedCommandDebugFramesRequiresAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "debug-frames")
	if res.cmdErr != "no client attached" {
		t.Fatalf("cmdErr = %q, want %q", res.cmdErr, "no client attached")
	}
}

func TestQueuedCommandDebugFramesForwardsThroughAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	serverConn, peerConn := net.Pipe()
	attached := newClientConn(serverConn)
	attached.ID = "client-1"
	t.Cleanup(func() {
		attached.Close()
		_ = peerConn.Close()
		_ = serverConn.Close()
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(attached)
	})

	requestCh := make(chan *Message, 1)
	errCh := make(chan error, 1)
	go func() {
		if err := peerConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			errCh <- err
			return
		}
		msg, err := readMsgOnConn(peerConn)
		if err != nil {
			errCh <- err
			return
		}
		requestCh <- msg
		sess.routeCaptureResponse(&Message{
			Type:      MsgTypeCaptureResponse,
			CmdOutput: "samples: 1\n",
		})
	}()

	res := runTestCommand(t, srv, sess, "debug-frames")
	if res.cmdErr != "" {
		t.Fatalf("cmdErr = %q, want empty", res.cmdErr)
	}
	if res.output != "samples: 1\n" {
		t.Fatalf("output = %q, want %q", res.output, "samples: 1\n")
	}

	select {
	case err := <-errCh:
		t.Fatalf("attached client read failed: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("request type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if !slices.Equal(msg.CmdArgs, []string{proto.ClientQueryDebugFramesArg}) {
			t.Fatalf("CmdArgs = %v, want [%q]", msg.CmdArgs, proto.ClientQueryDebugFramesArg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for forwarded debug frames request")
	}
}
