package remote

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestResolvePaneIDRequestsListPanesAndResolvesFreshName(t *testing.T) {
	t.Parallel()

	firstID := resolvePaneIDFromOneShotListServer(t, "remote-session", "agent", layoutWithWindows(
		windowWithPanes(paneDef{id: 10, name: "agent"}),
	))
	if firstID != 10 {
		t.Fatalf("first resolve ID = %d, want 10", firstID)
	}

	secondID := resolvePaneIDFromOneShotListServer(t, "remote-session", "agent", layoutWithWindows(
		windowWithPanes(paneDef{id: 20, name: "agent"}),
	))
	if secondID != 20 {
		t.Fatalf("second resolve ID = %d, want 20", secondID)
	}
}

func TestResolvePaneIDFromLayout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		layout  *proto.LayoutSnapshot
		pane    string
		wantID  uint32
		wantErr ResolvePaneIDErrorKind
	}{
		{
			name: "resolves pane in inactive window",
			layout: layoutWithWindows(
				windowWithPanes(paneDef{id: 1, name: "local"}),
				windowWithPanes(paneDef{id: 2, name: "remote"}),
			),
			pane:   "remote",
			wantID: 2,
		},
		{
			name: "resolves pane in legacy layout",
			layout: &proto.LayoutSnapshot{
				Root:  leafCell(7),
				Panes: []proto.PaneSnapshot{{ID: 7, Name: "legacy"}},
			},
			pane:   "legacy",
			wantID: 7,
		},
		{
			name: "ignores pane snapshots outside leaf layout",
			layout: &proto.LayoutSnapshot{
				Root:  leafCell(1),
				Panes: []proto.PaneSnapshot{{ID: 1, Name: "real"}, {ID: 99, Name: "orphan"}},
			},
			pane:    "orphan",
			wantErr: ResolvePaneIDNotFound,
		},
		{
			name: "missing pane",
			layout: layoutWithWindows(
				windowWithPanes(paneDef{id: 1, name: "alpha"}),
			),
			pane:    "beta",
			wantErr: ResolvePaneIDNotFound,
		},
		{
			name: "ambiguous pane",
			layout: layoutWithWindows(
				windowWithPanes(paneDef{id: 1, name: "same"}, paneDef{id: 2, name: "same"}),
			),
			pane:    "same",
			wantErr: ResolvePaneIDAmbiguous,
		},
		{
			name:    "nil layout",
			layout:  nil,
			pane:    "missing",
			wantErr: ResolvePaneIDNotFound,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolvePaneIDFromLayout(tt.layout, tt.pane)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ResolvePaneIDFromLayout() error = %v, want nil", err)
				}
				if got != tt.wantID {
					t.Fatalf("ResolvePaneIDFromLayout() ID = %d, want %d", got, tt.wantID)
				}
				return
			}

			var resolveErr *ResolvePaneIDError
			if !errors.As(err, &resolveErr) {
				t.Fatalf("ResolvePaneIDFromLayout() error = %T %[1]v, want ResolvePaneIDError", err)
			}
			if !strings.Contains(err.Error(), tt.pane) {
				t.Fatalf("ResolvePaneIDFromLayout() error = %q, want pane name %q", err, tt.pane)
			}
			if resolveErr.Kind != tt.wantErr {
				t.Fatalf("ResolvePaneIDError.Kind = %q, want %q", resolveErr.Kind, tt.wantErr)
			}
			if resolveErr.Name != tt.pane {
				t.Fatalf("ResolvePaneIDError.Name = %q, want %q", resolveErr.Name, tt.pane)
			}
		})
	}
}

func TestResolvePaneIDProtocolErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response *proto.Message
		want     string
	}{
		{
			name:     "server command error",
			response: &proto.Message{Type: proto.MsgTypeCmdResult, CmdErr: "no session"},
			want:     "no session",
		},
		{
			name:     "unexpected message",
			response: &proto.Message{Type: proto.MsgTypeNotify, Text: "not a layout"},
			want:     "unexpected",
		},
		{
			name:     "nil layout response",
			response: &proto.Message{Type: proto.MsgTypeLayout},
			want:     "nil layout",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientConn, requests := startResolveListServer(t, tt.response)
			_, err := ResolvePaneID(context.Background(), clientConn, "remote-session", "agent")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolvePaneID() error = %v, want substring %q", err, tt.want)
			}
			assertListPanesRequest(t, <-requests, "remote-session")
		})
	}
}

func TestResolvePaneIDConnectionErrors(t *testing.T) {
	t.Parallel()

	t.Run("nil connection", func(t *testing.T) {
		t.Parallel()

		_, err := ResolvePaneID(context.Background(), nil, "remote-session", "agent")
		if err == nil || !strings.Contains(err.Error(), "nil connection") {
			t.Fatalf("ResolvePaneID(nil) error = %v, want nil connection", err)
		}
	})

	t.Run("context canceled before request", func(t *testing.T) {
		t.Parallel()

		serverConn, clientConn := net.Pipe()
		t.Cleanup(func() { serverConn.Close() })
		t.Cleanup(func() { clientConn.Close() })

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := ResolvePaneID(ctx, clientConn, "remote-session", "agent")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ResolvePaneID() error = %v, want context.Canceled", err)
		}
	})

	t.Run("write error before request", func(t *testing.T) {
		t.Parallel()

		serverConn, clientConn := net.Pipe()
		serverConn.Close()
		clientConn.Close()

		_, err := ResolvePaneID(context.Background(), clientConn, "remote-session", "agent")
		if err == nil || !strings.Contains(err.Error(), "list panes") {
			t.Fatalf("ResolvePaneID() error = %v, want list panes write error", err)
		}
	})

	t.Run("context deadline during write", func(t *testing.T) {
		t.Parallel()

		serverConn, clientConn := net.Pipe()
		t.Cleanup(func() { serverConn.Close() })
		t.Cleanup(func() { clientConn.Close() })

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := ResolvePaneID(ctx, clientConn, "remote-session", "agent")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ResolvePaneID() error = %v, want context.DeadlineExceeded", err)
		}
	})

	t.Run("read error after request", func(t *testing.T) {
		t.Parallel()

		clientConn, requests := startResolveListServer(t, nil)
		_, err := ResolvePaneID(context.Background(), clientConn, "remote-session", "agent")
		if err == nil {
			t.Fatal("ResolvePaneID() error = nil, want read error")
		}
		assertListPanesRequest(t, <-requests, "remote-session")
	})

	t.Run("context deadline during read", func(t *testing.T) {
		t.Parallel()

		clientConn, requests := startResolveReadOnlyListServer(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := ResolvePaneID(ctx, clientConn, "remote-session", "agent")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ResolvePaneID() error = %v, want context.DeadlineExceeded", err)
		}
		assertListPanesRequest(t, <-requests, "remote-session")
	})
}

func resolvePaneIDFromOneShotListServer(t *testing.T, session, name string, layout *proto.LayoutSnapshot) uint32 {
	t.Helper()

	clientConn, requests := startResolveListServer(t, &proto.Message{Type: proto.MsgTypeLayout, Layout: layout})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, err := ResolvePaneID(ctx, clientConn, session, name)
	if err != nil {
		t.Fatalf("ResolvePaneID() error = %v, want nil", err)
	}
	assertListPanesRequest(t, <-requests, session)
	return got
}

func startResolveListServer(t *testing.T, response *proto.Message) (net.Conn, <-chan *proto.Message) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	requests := make(chan *proto.Message, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()

		_ = serverConn.SetDeadline(time.Now().Add(time.Second))
		request, err := proto.NewReader(serverConn).ReadMsg()
		if err == nil {
			requests <- request
		}
		close(requests)

		if response != nil {
			_ = proto.NewWriter(serverConn).WriteMsg(response)
		}
	}()

	t.Cleanup(func() {
		clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("list server did not exit")
		}
	})

	return clientConn, requests
}

func startResolveReadOnlyListServer(t *testing.T) (net.Conn, <-chan *proto.Message) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	requests := make(chan *proto.Message, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()

		request, err := proto.NewReader(serverConn).ReadMsg()
		if err == nil {
			requests <- request
		}
		close(requests)
		<-release
	}()

	t.Cleanup(func() {
		close(release)
		clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("read-only list server did not exit")
		}
	})

	return clientConn, requests
}

func assertListPanesRequest(t *testing.T, got *proto.Message, wantSession string) {
	t.Helper()

	if got == nil {
		t.Fatal("server received nil request")
	}
	if got.Type != proto.MsgTypeListPanes {
		t.Fatalf("request type = %v, want MsgTypeListPanes", got.Type)
	}
	if got.Session != wantSession {
		t.Fatalf("request session = %q, want %q", got.Session, wantSession)
	}
}

type paneDef struct {
	id   uint32
	name string
}

func layoutWithWindows(windows ...proto.WindowSnapshot) *proto.LayoutSnapshot {
	snap := &proto.LayoutSnapshot{Windows: windows}
	if len(windows) > 0 {
		snap.Root = windows[0].Root
		snap.Panes = append([]proto.PaneSnapshot(nil), windows[0].Panes...)
	}
	return snap
}

func windowWithPanes(panes ...paneDef) proto.WindowSnapshot {
	snapshots := make([]proto.PaneSnapshot, 0, len(panes))
	ids := make([]uint32, 0, len(panes))
	for _, pane := range panes {
		snapshots = append(snapshots, proto.PaneSnapshot{ID: pane.id, Name: pane.name})
		ids = append(ids, pane.id)
	}
	return proto.WindowSnapshot{Root: rootForLeafIDs(ids...), Panes: snapshots}
}

func rootForLeafIDs(ids ...uint32) proto.CellSnapshot {
	if len(ids) == 0 {
		return proto.CellSnapshot{IsLeaf: true}
	}
	if len(ids) == 1 {
		return leafCell(ids[0])
	}
	children := make([]proto.CellSnapshot, 0, len(ids))
	for _, id := range ids {
		children = append(children, leafCell(id))
	}
	return proto.CellSnapshot{Dir: 1, Children: children}
}

func leafCell(id uint32) proto.CellSnapshot {
	return proto.CellSnapshot{IsLeaf: true, Dir: -1, PaneID: id}
}
