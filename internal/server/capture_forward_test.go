package server

import (
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

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
	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	prevRetries := captureAttachMaxRetries
	captureAttachMaxRetries = 1
	defer func() {
		captureAttachMaxRetries = prevRetries
	}()

	resp := sess.forwardCapture([]string{"--format", "json"})
	if resp.CmdErr != "" {
		t.Fatalf("forwardCapture error: %s", resp.CmdErr)
	}
	assertJSONErrorResponse(t, resp.CmdOutput, "no_client_attached")
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

	serverConn, clientConn := net.Pipe()
	cc := NewClientConn(serverConn)
	defer cc.Close()
	defer clientConn.Close()

	var stateMu sync.Mutex
	var layout *proto.LayoutSnapshot
	layoutReady := make(chan struct{}, 1)
	captureReady := make(chan chan struct{}, 5)
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			msg, err := ReadMsg(clientConn)
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
					if err := WriteMsg(clientConn, &Message{Type: MsgTypeCaptureResponse, CmdOutput: string(data) + "\n"}); err != nil {
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
				if err := WriteMsg(clientConn, &Message{Type: MsgTypeCaptureResponse, CmdOutput: string(data) + "\n"}); err != nil {
					return
				}
			}
		}
	}()
	serverReadDone := make(chan struct{})
	go func() {
		defer close(serverReadDone)
		for {
			msg, err := ReadMsg(serverConn)
			if err != nil {
				return
			}
			if msg.Type == MsgTypeCaptureResponse {
				sess.routeCaptureResponse(msg)
			}
		}
	}()

	if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
		sess.clients = []*ClientConn{cc}
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
	defer conn.SetReadDeadline(time.Time{})

	msg, err := ReadMsg(conn)
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	return msg
}

func startForwardCaptureForTest(t *testing.T, sess *Session, args []string) (*Message, <-chan *Message) {
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
		sess.clients = []*clientConn{cc}
		return struct{}{}, nil
	}); err != nil {
		t.Fatalf("enqueueSessionQuery: %v", err)
	}

	respCh := make(chan *Message, 1)
	go func() {
		respCh <- sess.forwardCapture(args)
	}()

	return readCaptureRequestForTest(t, peerConn), respCh
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
