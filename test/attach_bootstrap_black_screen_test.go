package test

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"
)

func TestAttachWaitsToEnterAltScreenUntilBootstrapReady(t *testing.T) {
	t.Parallel()

	srv := newFakeAttachServer(t)
	defer srv.close()

	client := newPTYClientHarnessForSession(t, srv.session, srv.home, srv.coverDir)

	attach := srv.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}
	if attach.AttachMode != proto.AttachModeInteractive {
		t.Fatalf("attach mode = %v, want %v", attach.AttachMode, proto.AttachModeInteractive)
	}
	if client.waitForOutput(render.AltScreenEnter, 150*time.Millisecond) {
		t.Fatalf("client entered alt screen before bootstrap was ready, raw output:\n%s", client.outputString())
	}

	srv.sendLayout(t)
	srv.sendPaneOutput(t, 1, "left")
	srv.sendPaneOutput(t, 2, "right")
	if !client.waitForOutput(render.AltScreenEnter, time.Second) {
		t.Fatalf("client never entered alt screen after bootstrap completed, raw output:\n%s", client.outputString())
	}

	srv.sendExit(t)
	if !client.waitExited(5 * time.Second) {
		t.Fatalf("PTY client did not exit after server exit\nOutput:\n%s", client.outputString())
	}
	if err := client.waitError(); err != nil {
		t.Fatalf("PTY client exited with error: %v\nOutput:\n%s", err, client.outputString())
	}
}

func TestAttachRendersLayoutWhenBootstrapPaneReplayStalls(t *testing.T) {
	t.Parallel()

	srv := newFakeAttachServer(t)
	defer srv.close()

	client := newPTYClientHarnessForSession(t, srv.session, srv.home, srv.coverDir)

	attach := srv.waitAttach(t)
	if attach.Type != proto.MsgTypeAttach {
		t.Fatalf("attach type = %d, want %d", attach.Type, proto.MsgTypeAttach)
	}

	srv.sendLayout(t)
	srv.sendPaneOutput(t, 1, "left")
	if !client.waitForOutput("[pane-1]", time.Second) {
		t.Fatalf("client did not render layout while pane replay stalled, raw output:\n%s", client.outputString())
	}

	srv.sendPaneOutput(t, 2, "right")
	srv.sendExit(t)
	if !client.waitExited(5 * time.Second) {
		t.Fatalf("PTY client did not exit after server exit\nOutput:\n%s", client.outputString())
	}
	if err := client.waitError(); err != nil {
		t.Fatalf("PTY client exited with error: %v\nOutput:\n%s", err, client.outputString())
	}
}

type fakeAttachServer struct {
	t        *testing.T
	session  string
	home     string
	coverDir string
	sockPath string
	listener net.Listener
	conn     net.Conn
	attachCh chan *proto.Message
	errCh    chan error
}

func newFakeAttachServer(t *testing.T) *fakeAttachServer {
	t.Helper()

	home := t.TempDir()
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	session := fmt.Sprintf("attach-bootstrap-%x", suffix)
	sockPath := server.SocketPath(session)
	if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}

	srv := &fakeAttachServer{
		t:        t,
		session:  session,
		home:     home,
		coverDir: fakeAttachCoverDir(t, session),
		sockPath: sockPath,
		listener: listener,
		attachCh: make(chan *proto.Message, 1),
		errCh:    make(chan error, 1),
	}

	go srv.acceptLoop()

	t.Cleanup(srv.close)
	return srv
}

func fakeAttachCoverDir(t *testing.T, session string) string {
	t.Helper()
	if gocoverDir == "" {
		return ""
	}
	dir := filepath.Join(gocoverDir, session)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir cover dir: %v", err)
	}
	return dir
}

func (s *fakeAttachServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case s.errCh <- err:
			default:
			}
			return
		}

		msg, err := testProtoReader(conn).ReadMsg()
		if err != nil {
			_ = conn.Close()
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrWriteToConnected) || errors.Is(err, os.ErrDeadlineExceeded) {
				select {
				case s.errCh <- err:
				default:
				}
				return
			}
			continue
		}

		s.conn = conn
		select {
		case s.attachCh <- msg:
		default:
		}
		return
	}
}

func (s *fakeAttachServer) waitAttach(t *testing.T) *proto.Message {
	t.Helper()

	select {
	case msg := <-s.attachCh:
		return msg
	case err := <-s.errCh:
		t.Fatalf("fake attach server: %v", err)
		return nil
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for attach")
		return nil
	}
}

func (s *fakeAttachServer) sendLayout(t *testing.T) {
	t.Helper()
	s.writeMsg(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: fakeAttachLayoutSnapshot(s.session)})
}

func (s *fakeAttachServer) sendPaneOutput(t *testing.T, paneID uint32, content string) {
	t.Helper()
	s.writeMsg(t, &proto.Message{Type: proto.MsgTypePaneOutput, PaneID: paneID, PaneData: []byte(content)})
}

func (s *fakeAttachServer) sendExit(t *testing.T) {
	t.Helper()
	s.writeMsg(t, &proto.Message{Type: proto.MsgTypeExit})
}

func (s *fakeAttachServer) writeMsg(t *testing.T, msg *proto.Message) {
	t.Helper()
	if s.conn == nil {
		t.Fatal("fake attach server connection not ready")
	}
	if err := testProtoWriter(s.conn).WriteMsg(msg); err != nil {
		t.Fatalf("write fake attach message: %v", err)
	}
}

func (s *fakeAttachServer) close() {
	if s == nil {
		return
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.sockPath != "" {
		_ = os.Remove(s.sockPath)
	}
	select {
	case err := <-s.errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, os.ErrClosed) {
			s.t.Fatalf("fake attach server error: %v", err)
		}
	default:
	}
}

func fakeAttachLayoutSnapshot(session string) *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 23,
		Dir: int(mux.SplitVertical),
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}
	panes := []proto.PaneSnapshot{
		{ID: 1, Name: "pane-1", Host: "local", Color: config.AccentColor(0), ColumnIndex: 0},
		{ID: 2, Name: "pane-2", Host: "local", Color: config.AccentColor(1), ColumnIndex: 1},
	}
	return &proto.LayoutSnapshot{
		SessionName:  session,
		ActivePaneID: 1,
		Width:        80,
		Height:       23,
		Root:         root,
		Panes:        panes,
		Windows: []proto.WindowSnapshot{{
			ID: 1, Name: "window-1", Index: 1, ActivePaneID: 1,
			Root:  root,
			Panes: panes,
		}},
		ActiveWindowID: 1,
	}
}
