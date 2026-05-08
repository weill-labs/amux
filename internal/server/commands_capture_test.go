package server

import (
	"encoding/json"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
)

func TestCaptureDefaultSinglePaneDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Parallel()

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

func TestCaptureDefaultSinglePaneJSONDoesNotSendClientCaptureRequest(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.responseTimeout = 20 * time.Millisecond

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-JSON\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "--format", "json", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	var capturePane struct {
		Name    string   `json:"name"`
		Content []string `json:"content"`
	}
	if err := json.Unmarshal([]byte(res.output), &capturePane); err != nil {
		t.Fatalf("json.Unmarshal capture output: %v\n%s", err, res.output)
	}
	if capturePane.Name != "pane-1" {
		t.Fatalf("capture pane name = %q, want pane-1", capturePane.Name)
	}
	if len(capturePane.Content) == 0 || capturePane.Content[0] != "SERVER-JSON" {
		t.Fatalf("capture content = %#v, want SERVER-JSON as first row", capturePane.Content)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureDefaultSinglePaneWorksWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "capture", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-LOCAL\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}
}

func TestCaptureClientFlagForwardsToAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)
	requestCh, errCh := respondToNextCaptureRequest(t, sess, captureClient, "CLIENT-PANE\n")

	res := runTestCommand(t, srv, sess, "capture", "--client", "pane-1")
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
		if want := []string{"--client", "pane-1"}; !slices.Equal(msg.CmdArgs, want) {
			t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for forwarded capture request")
	}
}

func TestCaptureLegacyClientEnvForwardsSinglePaneToAttachedClient(t *testing.T) {
	t.Setenv("AMUX_CAPTURE_LEGACY_CLIENT", "1")

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

func TestCaptureClientFlagRequiresAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.FeedOutput([]byte("SERVER-LOCAL\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := runTestCommand(t, srv, sess, "capture", "--client", "pane-1")
	if got, want := res.cmdErr, "no client attached"; got != want {
		t.Fatalf("capture --client without client cmdErr = %q, want %q; output=%q", got, want, res.output)
	}
}

func TestCaptureFullSessionStillForwardsToAttachedClient(t *testing.T) {
	t.Parallel()

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

func TestCaptureHistoryPaneUsesServerHistoryPath(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.SetRetainedHistory([]string{"SERVER-HISTORY"})
	pane.FeedOutput([]byte("SERVER-VISIBLE"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	captureClient := attachCaptureClientForCommandTest(t, sess)

	res := runTestCommand(t, srv, sess, "capture", "--history", "pane-1")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	if got, want := res.output, "SERVER-HISTORY\nSERVER-VISIBLE\n"; got != want {
		t.Fatalf("capture output = %q, want %q", got, want)
	}

	if err := captureClient.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if msg, err := readMsgOnConn(captureClient); err == nil {
		t.Fatalf("attached client received %v, want no client capture request", msg.Type)
	}
}

func TestCaptureLocallyRejectsFullSessionDirectCall(t *testing.T) {
	t.Parallel()

	res := captureLocally(&CommandContext{}, nil)
	if got, want := res.CmdErr, "server-side full-session capture is not implemented"; got != want {
		t.Fatalf("captureLocally CmdErr = %q, want %q", got, want)
	}
}

func TestCaptureSinglePaneLocallyRejectsInvalidScreenFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  caputil.Request
		want string
	}{
		{
			name: "mutually exclusive formats",
			req: caputil.Request{
				PaneRef:     "pane-1",
				IncludeANSI: true,
				FormatJSON:  true,
			},
			want: "--ansi, --colors, and --format json are mutually exclusive",
		},
		{
			name: "pane color map",
			req: caputil.Request{
				PaneRef:  "pane-1",
				ColorMap: true,
			},
			want: "--colors is only supported for full screen capture",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := captureSinglePaneLocally(&CommandContext{}, tt.req)
			if got := res.CmdErr; got != tt.want {
				t.Fatalf("captureSinglePaneLocally CmdErr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaptureSinglePaneLocallyReturnsResolveError(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	res := captureSinglePaneLocally(&CommandContext{Sess: sess}, caputil.Request{PaneRef: "missing"})
	if got := res.CmdErr; !strings.Contains(got, "not found") {
		t.Fatalf("captureSinglePaneLocally CmdErr = %q, want pane not found", got)
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
