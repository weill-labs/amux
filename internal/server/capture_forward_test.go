package server

import (
	"net"
	"slices"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
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

			serverConn, clientConn := net.Pipe()
			defer clientConn.Close()
			cc := NewClientConn(serverConn)
			defer cc.Close()

			if _, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
				sess.clients = []*ClientConn{cc}
				return struct{}{}, nil
			}); err != nil {
				t.Fatalf("enqueueSessionQuery: %v", err)
			}

			respCh := make(chan *Message, 1)
			go func() {
				respCh <- sess.forwardCapture(tt.args)
			}()

			msg := readCaptureRequestForTest(t, clientConn)
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

			sess.routeCaptureResponse(&Message{
				Type:      MsgTypeCaptureResponse,
				CmdOutput: "ok",
			})

			select {
			case resp := <-respCh:
				if resp.CmdErr != "" {
					t.Fatalf("forwardCapture error: %s", resp.CmdErr)
				}
				if resp.CmdOutput != "ok" {
					t.Fatalf("forwardCapture output = %q, want ok", resp.CmdOutput)
				}
			case <-time.After(time.Second):
				t.Fatal("forwardCapture did not return")
			}
		})
	}
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
