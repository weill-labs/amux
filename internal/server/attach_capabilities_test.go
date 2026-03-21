package server

import (
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func TestHandleAttachStoresNegotiatedCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps *proto.ClientCapabilities
		want string
	}{
		{
			name: "legacy attach",
			caps: nil,
			want: "legacy",
		},
		{
			name: "partial modern attach",
			caps: &proto.ClientCapabilities{
				Hyperlinks:     true,
				PromptMarkers:  true,
				CursorMetadata: true,
			},
			want: "hyperlinks,cursor_metadata,prompt_markers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("test-attach-capabilities")
			stopCrashCheckpointLoop(t, sess)
			defer stopSessionBackgroundLoops(t, sess)

			pane := newProxyPane(1, mux.PaneMeta{
				Name:  "pane-1",
				Host:  mux.DefaultHost,
				Color: "f5e0dc",
			}, 80, 23, nil, nil, func(data []byte) (int, error) { return len(data), nil })
			pane.FeedOutput([]byte("hello from pane"))

			w := mux.NewWindow(pane, 80, 24-render.GlobalBarHeight)
			w.ID = 1
			w.Name = "window-1"
			sess.Windows = []*mux.Window{w}
			sess.ActiveWindowID = w.ID
			sess.Panes = []*mux.Pane{pane}

			srv := &Server{sessions: map[string]*Session{sess.Name: sess}}
			serverConn, clientConn := net.Pipe()
			defer clientConn.Close()

			done := make(chan struct{})
			go func() {
				defer close(done)
				srv.handleAttach(serverConn, &Message{
					Type:               MsgTypeAttach,
					Session:            sess.Name,
					Cols:               80,
					Rows:               24,
					AttachCapabilities: tt.caps,
				})
			}()

			readMsgWithTimeout(t, clientConn)
			readMsgWithTimeout(t, clientConn)
			readUntil(t, clientConn, func(msg *Message) bool {
				return msg.Type == MsgTypeLayout
			})

			clients, err := sess.queryClientList()
			if err != nil {
				t.Fatalf("queryClientList: %v", err)
			}
			if len(clients) != 1 {
				t.Fatalf("len(queryClientList) = %d, want 1", len(clients))
			}
			if got := clients[0].capabilities; got != tt.want {
				t.Fatalf("capabilities = %q, want %q", got, tt.want)
			}

			if err := WriteMsg(clientConn, &Message{Type: MsgTypeDetach}); err != nil {
				t.Fatalf("WriteMsg detach: %v", err)
			}

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handleAttach did not exit after detach")
			}
		})
	}
}
