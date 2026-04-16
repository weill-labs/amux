package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestForwardCaptureAgentStatusScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantIDs []uint32
		wantNil bool
	}{
		{
			name:    "full screen json includes all panes",
			args:    []string{"--format", "json"},
			wantIDs: []uint32{1, 2},
		},
		{
			name:    "single pane json includes requested pane only",
			args:    []string{"--format", "json", "pane-2"},
			wantIDs: []uint32{2},
		},
		{
			name:    "plain capture omits agent status",
			args:    []string{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			pane1 := newTestPane(sess, 1, "pane-1")
			pane2 := newTestPane(sess, 2, "pane-2")
			w := newTestWindowWithPanes(t, sess, 1, "window-1", pane1, pane2)
			if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
				sess.Windows = []*mux.Window{w}
				sess.ActiveWindowID = w.ID
				sess.Panes = []*mux.Pane{pane1, pane2}
				return struct{}{}, nil
			}); err != nil {
				t.Fatalf("enqueueSessionQuery: %v", err)
			}

			msg, respCh := startForwardCaptureForTest(t, sess, tt.args)
			if msg.Type != MsgTypeCaptureRequest {
				t.Fatalf("message type = %v, want capture request", msg.Type)
			}

			if tt.wantNil {
				if msg.AgentStatus != nil {
					t.Fatalf("agent status = %#v, want nil", msg.AgentStatus)
				}
			} else {
				gotIDs := make([]uint32, 0, len(msg.AgentStatus))
				for paneID := range msg.AgentStatus {
					gotIDs = append(gotIDs, paneID)
				}
				slices.Sort(gotIDs)
				if !slices.Equal(gotIDs, tt.wantIDs) {
					t.Fatalf("agent status pane IDs = %v, want %v", gotIDs, tt.wantIDs)
				}
			}

			deliverCaptureResponseForTest(t, sess, msg, respCh)
		})
	}
}

func TestForwardCaptureFullScreenJSONUsesActiveWindowPanesOnly(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newTestPane(sess, 2, "pane-2")
	pane3 := newTestPane(sess, 3, "pane-3")
	window1 := newTestWindowWithPanes(t, sess, 1, "window-1", pane1, pane2)
	window2 := newTestWindowWithPanes(t, sess, 2, "window-2", pane3)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window1, window2}
		sess.ActiveWindowID = window1.ID
		sess.Panes = []*mux.Pane{pane1, pane2, pane3}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	msg, respCh := startForwardCaptureForTest(t, sess, []string{"--format", "json"})
	gotIDs := make([]uint32, 0, len(msg.AgentStatus))
	for paneID := range msg.AgentStatus {
		gotIDs = append(gotIDs, paneID)
	}
	slices.Sort(gotIDs)
	if want := []uint32{1, 2}; !slices.Equal(gotIDs, want) {
		t.Fatalf("agent status pane IDs = %v, want %v", gotIDs, want)
	}

	deliverCaptureResponseForTest(t, sess, msg, respCh)
}

func TestForwardCaptureJSONIncludesIdleStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		advance       time.Duration
		wantIdle      bool
		wantIdleSince bool
	}{
		{
			name:          "settled pane reports idle timestamps",
			advance:       30 * time.Millisecond,
			wantIdle:      true,
			wantIdleSince: true,
		},
		{
			name:          "fresh output stays active",
			advance:       0,
			wantIdle:      false,
			wantIdleSince: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			clk := NewFakeClock(time.Date(2026, 3, 28, 12, 0, 0, 0, time.UTC))
			mustSessionMutation(t, sess, func(sess *Session) {
				sess.Clock = clk
				sess.ensureIdleTracker().VTIdleSettle = 20 * time.Millisecond
			})

			pane := newTestPane(sess, 1, "pane-1")
			window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
			setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

			mustSessionMutation(t, sess, func(sess *Session) {
				sess.ensureIdleTracker().TrackOutput(pane.ID, func() {}, func(time.Time) {})
			})
			clk.AwaitTimers(1)
			if tt.advance > 0 {
				clk.Advance(tt.advance)
			}

			msg, respCh := startForwardCaptureForTest(t, sess, []string{"--format", "json", "pane-1"})
			if msg.Type != MsgTypeCaptureRequest {
				t.Fatalf("message type = %v, want capture request", msg.Type)
			}
			status, ok := msg.AgentStatus[1]
			if !ok {
				t.Fatal("pane-1 missing from forwarded agent status")
			}
			if got := status.Idle; got != tt.wantIdle {
				t.Fatalf("Idle = %v, want %v", got, tt.wantIdle)
			}
			if got := status.LastOutput; got == "" {
				t.Fatalf("LastOutput = %q, want RFC3339 string", got)
			}
			if got := status.IdleSince; (got != "") != tt.wantIdleSince {
				t.Fatalf("IdleSince = %q, want present=%v", got, tt.wantIdleSince)
			}

			deliverCaptureResponseForTest(t, sess, msg, respCh)
		})
	}
}

func TestForwardCapturePaneFallsBackWithoutClient(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "pane-1")
	pane.FeedOutput([]byte("\x1b[31mHEADLESS-ANSI\x1b[m\r\nHEADLESS-PLAIN\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	tests := []struct {
		name string
		args []string
		want string
		json bool
	}{
		{name: "plain", args: []string{"pane-1"}, want: "HEADLESS-PLAIN"},
		{name: "json", args: []string{"--format", "json", "pane-1"}, want: "HEADLESS-PLAIN", json: true},
		{name: "ansi", args: []string{"--ansi", "pane-1"}, want: "\x1b[31m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: these subtests share a single session and pane fixture.
			resp := sess.capturePaneWithFallback(0, tt.args)
			if resp.CmdErr != "" {
				t.Fatalf("capturePaneWithFallback(%v) error: %s", tt.args, resp.CmdErr)
			}
			if tt.json {
				var pane proto.CapturePane
				if err := json.Unmarshal([]byte(resp.CmdOutput), &pane); err != nil {
					t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, resp.CmdOutput)
				}
				if pane.Name != "pane-1" {
					t.Fatalf("pane.Name = %q, want pane-1", pane.Name)
				}
				if joined := strings.Join(pane.Content, "\n"); !strings.Contains(joined, tt.want) {
					t.Fatalf("pane JSON missing %q\ncontent:\n%s", tt.want, joined)
				}
				return
			}
			if !strings.Contains(resp.CmdOutput, tt.want) {
				t.Fatalf("capturePaneWithFallback(%v) missing %q\noutput:\n%s", tt.args, tt.want, resp.CmdOutput)
			}
		})
	}
}

func TestForwardCapturePaneUsesResolvedNumericID(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "pane-1")
	pane.FeedOutput([]byte("FALLBACK-CLIENT-NOT-FOUND\r\n"))
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	msg, respCh := startCapturePaneWithFallbackForTest(t, sess, []string{"--format", "json", "pane-1"})
	if msg.Type != MsgTypeCaptureRequest {
		t.Fatalf("message type = %v, want capture request", msg.Type)
	}
	if want := []string{"--format", "json", "1"}; !slices.Equal(msg.CmdArgs, want) {
		t.Fatalf("capture request args = %v, want %v", msg.CmdArgs, want)
	}

	deliverCaptureResponseForTest(t, sess, msg, respCh)
}

func TestForwardCaptureJSONWrapsBadClientResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty output", raw: ""},
		{name: "empty object", raw: "{}\n"},
		{name: "invalid json", raw: "{not-json}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			pane1 := newTestPane(sess, 1, "pane-1")
			window := newTestWindowWithPanes(t, sess, 1, "window-1", pane1)
			if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
				sess.Windows = []*mux.Window{window}
				sess.ActiveWindowID = window.ID
				sess.Panes = []*mux.Pane{pane1}
				return struct{}{}, nil
			}); err != nil {
				t.Fatalf("enqueueSessionQuery: %v", err)
			}

			msg, respCh := startForwardCaptureForTest(t, sess, []string{"--format", "json"})
			if msg.Type != MsgTypeCaptureRequest {
				t.Fatalf("message type = %v, want capture request", msg.Type)
			}

			sess.routeCaptureResponse(&Message{
				Type:      MsgTypeCaptureResponse,
				CmdOutput: tt.raw,
			})

			select {
			case resp := <-respCh:
				if resp.CmdErr != "" {
					t.Fatalf("forwardCapture error: %s", resp.CmdErr)
				}
				assertJSONErrorResponse(t, resp.CmdOutput, "invalid_capture_response")
			case <-time.After(time.Second):
				t.Fatal("forwardCapture did not return")
			}
		})
	}
}

func TestForwardCaptureJSONNoClientReturnsErrorObject(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sess.captureTiming.attachMaxRetries = 1

	resp := sess.forwardCapture([]string{"--format", "json"})
	if resp.CmdErr != "" {
		t.Fatalf("forwardCapture error: %s", resp.CmdErr)
	}
	assertJSONErrorResponse(t, resp.CmdOutput, "no_client_attached")
}

func TestForwardCaptureJSONNoClientRetriesBeforeErrorObject(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sess.captureTiming.attachMaxRetries = 2
	sess.captureTiming.attachRetryDelay = time.Nanosecond

	resp := sess.forwardCapture([]string{"--format", "json"})
	if resp.CmdErr != "" {
		t.Fatalf("forwardCapture error: %s", resp.CmdErr)
	}
	assertJSONErrorResponse(t, resp.CmdOutput, "no_client_attached")
}

type blockingWriteConn struct {
	writeStarted sync.Once
	writeSeen    chan struct{}
	release      chan struct{}
	closed       chan struct{}
}

func newBlockingWriteConn() *blockingWriteConn {
	return &blockingWriteConn{
		writeSeen: make(chan struct{}),
		release:   make(chan struct{}),
		closed:    make(chan struct{}),
	}
}

func (c *blockingWriteConn) Read(_ []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
		return 0, io.EOF
	}
}

func (c *blockingWriteConn) Write(p []byte) (int, error) {
	c.writeStarted.Do(func() { close(c.writeSeen) })
	select {
	case <-c.release:
		return len(p), nil
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *blockingWriteConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	select {
	case <-c.release:
	default:
		close(c.release)
	}
	return nil
}

func (c *blockingWriteConn) LocalAddr() net.Addr              { return dummyNetAddr("local") }
func (c *blockingWriteConn) RemoteAddr() net.Addr             { return dummyNetAddr("remote") }
func (c *blockingWriteConn) SetDeadline(time.Time) error      { return nil }
func (c *blockingWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (c *blockingWriteConn) SetWriteDeadline(time.Time) error { return nil }

type dummyNetAddr string

func (a dummyNetAddr) Network() string { return "test" }
func (a dummyNetAddr) String() string  { return string(a) }

func blockClientWriterForTest(t *testing.T, id string) (*clientConn, func()) {
	t.Helper()

	blockedConn := newBlockingWriteConn()
	cc := newClientConn(blockedConn)
	cc.ID = id

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: strings.Repeat("x", 1<<20)})
	}()

	select {
	case <-blockedConn.writeSeen:
	case <-time.After(time.Second):
		t.Fatal("blocked client writer did not start write")
	}

	cleanup := func() {
		close(blockedConn.release)
		select {
		case err := <-sendDone:
			if err != nil {
				t.Fatalf("blocked client send error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked client send did not finish after release")
		}
		cc.Close()
	}

	return cc, cleanup
}

func captureForwarderSnapshotForTest(t *testing.T, sess *Session) captureForwarderState {
	t.Helper()
	return mustSessionQuery(t, sess, func(sess *Session) captureForwarderState {
		return sess.ensureCaptureForwarder().snapshot()
	})
}

func assertSessionEventLoopResponsive(t *testing.T, sess *Session) {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
			return struct{}{}, nil
		})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("event loop query failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session event loop blocked")
	}
}

func TestEnqueueCaptureRequestBlockedWriterDoesNotFreezeEventLoop(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	t.Cleanup(cleanup)

	blocked, releaseBlocked := blockClientWriterForTest(t, "client-blocked")
	t.Cleanup(releaseBlocked)

	req := &captureRequest{
		id:     1,
		client: blocked,
		reply:  make(chan *Message, 1),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- sess.enqueueCaptureRequest(req)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("enqueueCaptureRequest: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueueCaptureRequest blocked on a stuck client writer")
	}

	state := captureForwarderSnapshotForTest(t, sess)
	if !state.hasCurrent || state.currentID != req.id {
		t.Fatalf("forwarder state = %+v, want current request %d", state, req.id)
	}
	assertSessionEventLoopResponsive(t, sess)
}

func TestRouteCaptureResponseQueuedBlockedWriterDoesNotFreezeEventLoop(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	t.Cleanup(cleanup)

	ready := newClientConn(discardConn{})
	t.Cleanup(ready.Close)

	blocked, releaseBlocked := blockClientWriterForTest(t, "client-blocked")
	t.Cleanup(releaseBlocked)

	req1 := &captureRequest{
		id:     1,
		client: ready,
		reply:  make(chan *Message, 1),
	}
	req2 := &captureRequest{
		id:     2,
		client: blocked,
		reply:  make(chan *Message, 1),
	}

	if err := sess.enqueueCaptureRequest(req1); err != nil {
		t.Fatalf("enqueueCaptureRequest(req1): %v", err)
	}
	if err := sess.enqueueCaptureRequest(req2); err != nil {
		t.Fatalf("enqueueCaptureRequest(req2): %v", err)
	}

	resp := &Message{Type: MsgTypeCaptureResponse, CmdOutput: "ok"}
	done := make(chan struct{})
	go func() {
		sess.routeCaptureResponse(resp)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("routeCaptureResponse blocked while starting the next capture request")
	}

	select {
	case got := <-req1.reply:
		if got != resp {
			t.Fatalf("req1 reply = %#v, want %#v", got, resp)
		}
	case <-time.After(time.Second):
		t.Fatal("current request did not receive the routed response")
	}

	state := captureForwarderSnapshotForTest(t, sess)
	if !state.hasCurrent || state.currentID != req2.id {
		t.Fatalf("forwarder state = %+v, want current request %d", state, req2.id)
	}
	assertSessionEventLoopResponsive(t, sess)
}

func TestCancelCaptureRequestQueuedBlockedWriterDoesNotFreezeEventLoop(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	t.Cleanup(cleanup)

	ready := newClientConn(discardConn{})
	t.Cleanup(ready.Close)

	blocked, releaseBlocked := blockClientWriterForTest(t, "client-blocked")
	t.Cleanup(releaseBlocked)

	req1 := &captureRequest{
		id:     1,
		client: ready,
		reply:  make(chan *Message, 1),
	}
	req2 := &captureRequest{
		id:     2,
		client: blocked,
		reply:  make(chan *Message, 1),
	}

	if err := sess.enqueueCaptureRequest(req1); err != nil {
		t.Fatalf("enqueueCaptureRequest(req1): %v", err)
	}
	if err := sess.enqueueCaptureRequest(req2); err != nil {
		t.Fatalf("enqueueCaptureRequest(req2): %v", err)
	}

	done := make(chan struct{})
	go func() {
		sess.cancelCaptureRequest(req1.id)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelCaptureRequest blocked while starting the next capture request")
	}

	state := captureForwarderSnapshotForTest(t, sess)
	if !state.hasCurrent || state.currentID != req2.id {
		t.Fatalf("forwarder state = %+v, want current request %d", state, req2.id)
	}
	assertSessionEventLoopResponsive(t, sess)
}

func TestCaptureClientSnapshotSkipsBootstrappingClientWithoutWriterRoundTrip(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	blockedConn := newBlockingWriteConn()
	blocked := newClientConn(blockedConn)
	blocked.ID = "client-1"
	t.Cleanup(blocked.Close)
	blocked.startBootstrap()

	sendDone := make(chan error, 1)
	go func() {
		sendDone <- blocked.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: strings.Repeat("x", 1<<20)})
	}()

	select {
	case <-blockedConn.writeSeen:
	case <-time.After(time.Second):
		t.Fatal("blocked client writer did not start write")
	}

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { peerConn.Close() })

	ready := newClientConn(serverConn)
	t.Cleanup(ready.Close)

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.ensureClientManager().setClientsForTest(blocked, ready)
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	done := make(chan struct{})
	var (
		snap captureClientSnapshot
		err  error
	)
	go func() {
		snap, err = sess.captureClientSnapshotForActor(caputil.Request{}, 0, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("captureClientSnapshotForActor blocked on bootstrapping client")
	}
	if err != nil {
		t.Fatalf("captureClientSnapshotForActor: %v", err)
	}
	if snap.client != ready {
		t.Fatalf("capture client = %#v, want ready client", snap.client)
	}

	close(blockedConn.release)
	select {
	case sendErr := <-sendDone:
		if sendErr != nil {
			t.Fatalf("blocked client send error: %v", sendErr)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked client send did not finish after release")
	}
}

func TestForwardCaptureJSONReturnsSessionShuttingDownBeforeAttach(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	done := make(chan struct{})
	close(stop)
	close(done)

	sess := &Session{
		sessionEventStop: stop,
		sessionEventDone: done,
	}

	resp := sess.forwardCapture([]string{"--format", "json"})
	if resp.CmdErr != "" {
		t.Fatalf("forwardCapture error: %s", resp.CmdErr)
	}
	assertJSONErrorResponse(t, resp.CmdOutput, "session_shutting_down")
}

func TestForwardCaptureJSONHandlesNilAndErrResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		routeResponse func(*Session)
		assert        func(*testing.T, *Message)
	}{
		{
			name: "nil response",
			routeResponse: func(sess *Session) {
				sess.routeCaptureResponse(nil)
			},
			assert: func(t *testing.T, resp *Message) {
				t.Helper()
				if resp.CmdErr != "" {
					t.Fatalf("forwardCapture error: %s", resp.CmdErr)
				}
				assertJSONErrorResponse(t, resp.CmdOutput, "invalid_capture_response")
			},
		},
		{
			name: "cmd err",
			routeResponse: func(sess *Session) {
				sess.routeCaptureResponse(&Message{Type: MsgTypeCaptureResponse, CmdErr: "boom"})
			},
			assert: func(t *testing.T, resp *Message) {
				t.Helper()
				if resp.CmdErr != "boom" {
					t.Fatalf("forwardCapture cmdErr = %q, want boom", resp.CmdErr)
				}
				if resp.CmdOutput != "" {
					t.Fatalf("forwardCapture output = %q, want empty", resp.CmdOutput)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, sess, cleanup := newCommandTestSession(t)
			defer cleanup()

			msg, respCh := startForwardCaptureForTest(t, sess, []string{"--format", "json"})
			if msg.Type != MsgTypeCaptureRequest {
				t.Fatalf("message type = %v, want capture request", msg.Type)
			}

			tt.routeResponse(sess)

			select {
			case resp := <-respCh:
				tt.assert(t, resp)
			case <-time.After(time.Second):
				t.Fatal("forwardCapture did not return")
			}
		})
	}
}

func TestForwardCaptureJSONTimeoutReturnsErrorObject(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	sess.captureTiming.responseTimeout = time.Millisecond

	msg, respCh := startForwardCaptureForTest(t, sess, []string{"--format", "json"})
	if msg.Type != MsgTypeCaptureRequest {
		t.Fatalf("message type = %v, want capture request", msg.Type)
	}

	select {
	case resp := <-respCh:
		if resp.CmdErr != "" {
			t.Fatalf("forwardCapture error: %s", resp.CmdErr)
		}
		assertJSONErrorResponse(t, resp.CmdOutput, "capture_timeout")
	case <-time.After(time.Second):
		t.Fatal("forwardCapture did not return")
	}
}

func TestForwardCaptureJSONReturnsSessionShuttingDownWhileWaiting(t *testing.T) {
	t.Parallel()

	sess := newSession("test-forward-capture-shutdown")
	stopCrashCheckpointLoop(t, sess)
	stopped := false
	defer func() {
		if !stopped {
			stopSessionBackgroundLoops(t, sess)
		}
	}()

	serverConn, clientEnd := net.Pipe()
	cc := newClientConn(serverConn)
	defer cc.Close()
	defer clientEnd.Close()

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	respCh := make(chan *Message, 1)
	go func() {
		respCh <- sess.forwardCapture([]string{"--format", "json"})
	}()

	msg := readCaptureRequestForTest(t, clientEnd)
	if msg.Type != MsgTypeCaptureRequest {
		t.Fatalf("message type = %v, want capture request", msg.Type)
	}

	close(sess.sessionEventStop)
	<-sess.sessionEventDone
	stopped = true

	select {
	case resp := <-respCh:
		if resp.CmdErr != "" {
			t.Fatalf("forwardCapture error: %s", resp.CmdErr)
		}
		assertJSONErrorResponse(t, resp.CmdOutput, "session_shutting_down")
	case <-time.After(time.Second):
		t.Fatal("forwardCapture did not return")
	}
}

func TestForwardCaptureJSONStressUnderPaneOutput(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane1 := newTestPane(sess, 1, "pane-1")
	pane2 := newTestPane(sess, 2, "pane-2")
	pane3 := newTestPane(sess, 3, "pane-3")
	pane4 := newTestPane(sess, 4, "pane-4")
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane1, pane2, pane3, pane4)
	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.Panes = []*mux.Pane{pane1, pane2, pane3, pane4}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	serverConn, clientEnd := net.Pipe()
	cc := newClientConn(serverConn)
	defer cc.Close()
	defer clientEnd.Close()

	var stateMu sync.Mutex
	var layout *proto.LayoutSnapshot
	layoutReady := make(chan struct{}, 1)
	captureReady := make(chan chan struct{}, 5)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			msg, err := readMsgOnConn(clientEnd)
			if err != nil {
				return
			}
			switch msg.Type {
			case MsgTypeLayout:
				stateMu.Lock()
				layout = msg.Layout
				stateMu.Unlock()
				select {
				case layoutReady <- struct{}{}:
				default:
				}
			case MsgTypeCaptureRequest:
				stateMu.Lock()
				snap := layout
				stateMu.Unlock()
				responseGate := make(chan struct{})
				captureReady <- responseGate
				<-responseGate
				if snap == nil {
					errResp := proto.CaptureJSON{
						Error: &proto.CaptureError{
							Code:    "state_unavailable",
							Message: "capture layout not ready",
						},
					}
					data, _ := json.MarshalIndent(errResp, "", "  ")
					if err := writeMsgOnConn(clientEnd, &Message{Type: MsgTypeCaptureResponse, CmdOutput: string(data) + "\n"}); err != nil {
						return
					}
					continue
				}
				capture := proto.CaptureJSON{
					Session: snap.SessionName,
					Width:   snap.Width,
					Height:  snap.Height + 1,
				}
				for _, pane := range snap.Panes {
					capture.Panes = append(capture.Panes, proto.CapturePane{
						ID:      pane.ID,
						Name:    pane.Name,
						Content: []string{},
					})
				}
				data, _ := json.MarshalIndent(capture, "", "  ")
				if err := writeMsgOnConn(clientEnd, &Message{Type: MsgTypeCaptureResponse, CmdOutput: string(data) + "\n"}); err != nil {
					return
				}
			}
		}
	}()
	serverReadDone := make(chan struct{})
	go func() {
		defer close(serverReadDone)
		for {
			msg, err := readMsgOnConn(serverConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCaptureResponse {
				sess.routeCaptureResponse(msg)
			}
		}
	}()

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}
	sess.broadcastLayout()

	select {
	case <-layoutReady:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial layout")
	}

	panes := []*mux.Pane{pane1, pane2, pane3, pane4}
	started := make(chan uint32, len(panes))
	const captureIterations = 5
	const writesPerCapture = 3
	var writers sync.WaitGroup
	stepChans := make([]chan struct{}, 0, len(panes))
	for idx, pane := range panes {
		stepCh := make(chan struct{}, captureIterations*writesPerCapture)
		stepChans = append(stepChans, stepCh)
		writers.Add(1)
		go func(p *mux.Pane, label string, steps <-chan struct{}) {
			defer writers.Done()
			totalWrites := 1 + captureIterations*writesPerCapture
			for i := 0; i < totalWrites; i++ {
				if i > 0 {
					<-steps
				}
				p.FeedOutput([]byte(fmt.Sprintf("%s-%03d\n", label, i)))
				if i == 0 {
					started <- p.ID
				}
			}
		}(pane, fmt.Sprintf("LOAD%d", idx+1), stepCh)
	}

	for range panes {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for pane writers to start")
		}
	}

	for i := 0; i < captureIterations; i++ {
		results := make(chan struct {
			output string
			cmdErr string
		}, 1)
		go func() {
			results <- runTestCommand(t, srv, sess, "capture", "--format", "json")
		}()

		var responseGate chan struct{}
		select {
		case responseGate = <-captureReady:
		case <-time.After(time.Second):
			t.Fatalf("capture iteration %d did not reach fake client", i)
		}
		for step := 0; step < writesPerCapture; step++ {
			for _, steps := range stepChans {
				steps <- struct{}{}
			}
		}
		close(responseGate)

		result := <-results
		if result.cmdErr != "" {
			t.Fatalf("capture iteration %d returned error: %s", i, result.cmdErr)
		}
		if result.output == "" {
			t.Fatalf("capture iteration %d returned empty output", i)
		}

		var capture proto.CaptureJSON
		if err := json.Unmarshal([]byte(result.output), &capture); err != nil {
			t.Fatalf("capture iteration %d json.Unmarshal: %v\nraw: %s", i, err, result.output)
		}
		if capture.Error != nil {
			t.Fatalf("capture iteration %d returned capture error %+v\nraw: %s", i, *capture.Error, result.output)
		}
		if len(capture.Panes) != len(panes) {
			t.Fatalf("capture iteration %d panes = %d, want %d", i, len(capture.Panes), len(panes))
		}
	}

	writers.Wait()
	cc.Close()
	<-serverReadDone
	<-clientDone
}

func readCaptureRequestForTest(t *testing.T, conn net.Conn) *Message {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer mustSetReadDeadline(t, conn, time.Time{})

	msg, err := readMsgOnConn(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	return msg
}

func startCaptureCallForTest(t *testing.T, sess *Session, call func() *Message) (*Message, <-chan *Message) {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() {
		peerConn.Close()
		serverConn.Close()
	})
	cc := newClientConn(serverConn)
	t.Cleanup(func() {
		cc.Close()
	})

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.ensureClientManager().setClientsForTest(cc)
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	respCh := make(chan *Message, 1)
	go func() {
		respCh <- call()
	}()

	return readCaptureRequestForTest(t, peerConn), respCh
}

func startForwardCaptureForTest(t *testing.T, sess *Session, args []string) (*Message, <-chan *Message) {
	t.Helper()
	return startCaptureCallForTest(t, sess, func() *Message {
		return sess.forwardCapture(args)
	})
}

func startCapturePaneWithFallbackForTest(t *testing.T, sess *Session, args []string) (*Message, <-chan *Message) {
	t.Helper()
	return startCaptureCallForTest(t, sess, func() *Message {
		return sess.capturePaneWithFallback(0, args)
	})
}

func deliverCaptureResponseForTest(t *testing.T, sess *Session, req *Message, respCh <-chan *Message) {
	t.Helper()

	output := "ok"
	if captureReq := req; captureReq != nil && captureRequestIsJSON(captureReq) {
		if captureReqHasPaneRef(captureReq) {
			output = "{\n  \"id\": 1,\n  \"name\": \"pane-1\",\n  \"content\": []\n}\n"
		} else {
			output = "{\n  \"session\": \"test\",\n  \"window\": {\n    \"id\": 1,\n    \"name\": \"window-1\",\n    \"index\": 1\n  },\n  \"width\": 80,\n  \"height\": 24,\n  \"panes\": []\n}\n"
		}
	}
	sess.routeCaptureResponse(&Message{
		Type:      MsgTypeCaptureResponse,
		CmdOutput: output,
	})

	select {
	case resp := <-respCh:
		if resp.CmdErr != "" {
			t.Fatalf("forwardCapture error: %s", resp.CmdErr)
		}
		if resp.CmdOutput != output {
			t.Fatalf("forwardCapture output = %q, want %q", resp.CmdOutput, output)
		}
	case <-time.After(time.Second):
		t.Fatal("forwardCapture did not return")
	}
}

func captureRequestIsJSON(msg *Message) bool {
	return msg != nil && slices.Contains(msg.CmdArgs, "--format") && slices.Contains(msg.CmdArgs, "json")
}

func captureReqHasPaneRef(msg *Message) bool {
	if msg == nil {
		return false
	}
	req := msg.CmdArgs
	for i := 0; i < len(req); i++ {
		switch req[i] {
		case "--ansi", "--colors", "--display", "--history":
		case "--format":
			if i+1 < len(req) {
				i++
			}
		default:
			return true
		}
	}
	return false
}

func assertJSONErrorResponse(t *testing.T, raw, wantCode string) {
	t.Helper()

	var capture struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &capture); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", raw, err)
	}
	if capture.Error == nil {
		t.Fatalf("expected JSON error response, got: %q", raw)
	}
	if capture.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q", capture.Error.Code, wantCode)
	}
	if capture.Error.Message == "" {
		t.Fatal("error message should be non-empty")
	}
}

func TestEnsureTrailingNewline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "appends newline", in: "json", want: "json\n"},
		{name: "preserves newline", in: "json\n", want: "json\n"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ensureTrailingNewline(tt.in); got != tt.want {
				t.Fatalf("ensureTrailingNewline(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
