package server

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCaptureServerEnvSinglePaneDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Setenv("AMUX_CAPTURE_SERVER", "1")

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-LOCAL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureDefaultSinglePaneStillForwardsToAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-PANE\n")

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-PANE\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if want := []string{"1"}; !slices.Equal(msg.CmdArgs, want) {
			t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureServerEnvFullSessionStillForwardsInStepOne(t *testing.T) {
	t.Setenv("AMUX_CAPTURE_SERVER", "1")

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-FULL\n")

	res := runTestCommand(t, srv, sess, "capture")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "CLIENT-FULL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	select {
	case err := <-errCh:
		t.Fatalf("reading capture request: %v", err)
	case msg := <-requestCh:
		if msg.Type != MsgTypeCaptureRequest {
			t.Fatalf("message type = %v, want %v", msg.Type, MsgTypeCaptureRequest)
		}
		if len(msg.CmdArgs) != 0 {
			t.Fatalf("capture request args = %v, want empty", msg.CmdArgs)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func attachCaptureClientForCommandTest(t *testing.T, sess *Session) net.Conn {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	attached := newClientConn(serverConn)
	attached.ID = "client-capture"
	t.Cleanup(func() {
		attached.Close()
		_ = peerConn.Close()
		_ = serverConn.Close()
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.ensureClientManager().setClientsForTest(attached)
	})
	return peerConn
}

func respondToNextCaptureRequest(t *testing.T, sess *Session, conn net.Conn, output string) (<-chan *Message, <-chan error) {
	t.Helper()

	requestCh := make(chan *Message, 1)
	errCh := make(chan error, 1)
	go func() {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			errCh <- err
			return
		}
		msg, err := readMsgOnConn(conn)
		if err != nil {
			errCh <- err
			return
		}
		requestCh <- msg
		sess.routeCaptureResponse(&Message{
			Type:      MsgTypeCaptureResponse,
			CmdOutput: output,
		})
	}()
	return requestCh, errCh
}
