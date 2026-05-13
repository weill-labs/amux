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
	"github.com/weill-labs/amux/internal/proto"
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

func TestCaptureFullSessionTextUsesServerLayoutWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	pane1.FeedOutput([]byte("LEFT-SERVER\r\n"))
	pane2.FeedOutput([]byte("RIGHT-SERVER\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	res := runTestCommand(t, srv, sess, "capture")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	for _, want := range []string{"LEFT-SERVER", "RIGHT-SERVER", "[pane-1]", "[pane-2]", "test-command-queue"} {
		if !strings.Contains(res.output, want) {
			t.Fatalf("capture output missing %q:\n%s", want, res.output)
		}
	}
}

func TestCaptureFullSessionJSONUsesServerPaneMetadataWithoutAttachedClient(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()
	sess.captureTiming.attachMaxRetries = 1

	pane1 := newStandaloneProxyPane(1, "pane-1")
	pane2 := newStandaloneProxyPane(2, "pane-2")
	pane1.FeedOutput([]byte("JSON-SERVER-ONE\r\n"))
	pane2.FeedOutput([]byte("JSON-SERVER-TWO\r\n"))
	pane1.Meta.Task = "ship"
	pane1.Meta.GitBranch = "feature/full-session-capture"
	pane1.Meta.PR = "1760"
	pane1.Meta.KV = map[string]string{"issue": "LAB-1760", "owner": "codex"}
	pane1.Meta.TrackedPRs = []proto.TrackedPR{{Number: 1760, Status: proto.TrackedStatusActive}}
	pane1.Meta.TrackedIssues = []proto.TrackedIssue{{ID: "LAB-1760", Status: proto.TrackedStatusActive}}
	window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane1, pane2)

	res := runTestCommand(t, srv, sess, "capture", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture cmdErr = %q, want empty", res.cmdErr)
	}
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(res.output), &capture); err != nil {
		t.Fatalf("json.Unmarshal capture output: %v\n%s", err, res.output)
	}
	if capture.Error != nil {
		t.Fatalf("capture error = %+v, want nil", capture.Error)
	}
	if capture.Session != "test-command-queue" {
		t.Fatalf("capture session = %q, want test-command-queue", capture.Session)
	}
	if capture.Window.ID != 1 || capture.Window.Name != "main" || capture.Window.Index != 1 {
		t.Fatalf("capture window = %+v, want active main window", capture.Window)
	}
	if len(capture.Panes) != 2 {
		t.Fatalf("capture panes = %d, want 2: %+v", len(capture.Panes), capture.Panes)
	}

	var pane *proto.CapturePane
	for i := range capture.Panes {
		if capture.Panes[i].Position == nil {
			t.Fatalf("pane %s missing position", capture.Panes[i].Name)
		}
		if capture.Panes[i].Name == "pane-1" {
			pane = &capture.Panes[i]
		}
	}
	if pane == nil {
		t.Fatalf("pane-1 missing from capture: %+v", capture.Panes)
	}
	if pane.Task != "ship" || pane.Meta.Task != "ship" {
		t.Fatalf("pane task fields = top-level %q meta %q, want ship", pane.Task, pane.Meta.Task)
	}
	if pane.GitBranch != "feature/full-session-capture" || pane.Meta.GitBranch != "feature/full-session-capture" {
		t.Fatalf("pane git branch fields = top-level %q meta %q", pane.GitBranch, pane.Meta.GitBranch)
	}
	if pane.PR != "1760" || pane.Meta.PR != "1760" {
		t.Fatalf("pane PR fields = top-level %q meta %q", pane.PR, pane.Meta.PR)
	}
	if got := pane.Meta.KV["issue"]; got != "LAB-1760" {
		t.Fatalf("pane meta issue = %q, want LAB-1760", got)
	}
	if len(pane.Meta.TrackedPRs) != 1 || pane.Meta.TrackedPRs[0].Number != 1760 {
		t.Fatalf("pane tracked PRs = %+v, want PR 1760", pane.Meta.TrackedPRs)
	}
	if len(pane.Meta.TrackedIssues) != 1 || pane.Meta.TrackedIssues[0].ID != "LAB-1760" {
		t.Fatalf("pane tracked issues = %+v, want LAB-1760", pane.Meta.TrackedIssues)
	}
	if joined := strings.Join(pane.Content, "\n"); !strings.Contains(joined, "JSON-SERVER-ONE") {
		t.Fatalf("pane content = %q, want JSON-SERVER-ONE", joined)
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
