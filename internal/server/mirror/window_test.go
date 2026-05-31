package mirror

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

func TestManagerTrackWindowDeliversLayoutUpdates(t *testing.T) {
	t.Parallel()

	got := make(chan *proto.LayoutSnapshot, 4)
	dialer := &pipeDialer{conns: make(chan net.Conn, 1)}
	mgr := NewManager(Config{
		Hosts:       map[string]config.Host{"h": {SSH: "x", Session: "main", SocketPath: "/tmp/x"}},
		Dialer:      dialer,
		RetryPolicy: remoteRetryPolicyForTest(2),
		OnWindowLayout: func(_ uint32, _ WindowRef, layout *proto.LayoutSnapshot) {
			got <- layout
		},
	})
	t.Cleanup(mgr.Close)

	if err := mgr.TrackWindow(7, WindowRef{Host: "h", Session: "main", WindowName: "amux"}); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}

	serverConn := <-dialer.conns
	t.Cleanup(func() { _ = serverConn.Close() })

	sub, err := proto.NewReader(serverConn).ReadMsg()
	if err != nil {
		t.Fatalf("read subscription: %v", err)
	}
	if sub.Type != proto.MsgTypeAttachWindow || sub.WindowName != "amux" {
		t.Fatalf("subscription = type %d name %q, want AttachWindow amux", sub.Type, sub.WindowName)
	}

	if err := proto.NewWriter(serverConn).WriteMsg(&proto.Message{
		Type:   proto.MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{Windows: []proto.WindowSnapshot{{ID: 1, Name: "amux"}}},
	}); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	select {
	case layout := <-got:
		if len(layout.Windows) != 1 || layout.Windows[0].Name != "amux" {
			t.Fatalf("unexpected layout: %+v", layout)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnWindowLayout was not called")
	}
}

func TestManagerTrackWindowValidation(t *testing.T) {
	t.Parallel()

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)

	tests := []struct {
		name string
		id   uint32
		ref  WindowRef
	}{
		{name: "zero id", id: 0, ref: WindowRef{Host: "h", WindowName: "w"}},
		{name: "no host", id: 1, ref: WindowRef{WindowName: "w"}},
		{name: "no window name", id: 1, ref: WindowRef{Host: "h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := mgr.TrackWindow(tt.id, tt.ref); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}

func TestManagerDetachWindowClosesLink(t *testing.T) {
	t.Parallel()

	dialer := &pipeDialer{conns: make(chan net.Conn, 1)}
	mgr := NewManager(Config{
		Hosts:       map[string]config.Host{"h": {SSH: "x", Session: "main", SocketPath: "/tmp/x"}},
		Dialer:      dialer,
		RetryPolicy: remoteRetryPolicyForTest(2),
	})
	t.Cleanup(mgr.Close)

	if err := mgr.TrackWindow(7, WindowRef{Host: "h", Session: "main", WindowName: "amux"}); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}
	serverConn := <-dialer.conns
	t.Cleanup(func() { _ = serverConn.Close() })

	// Drain the subscription so the link is fully connected.
	if _, err := proto.NewReader(serverConn).ReadMsg(); err != nil {
		t.Fatalf("read subscription: %v", err)
	}

	mgr.DetachWindow(7)

	// After detach, the remote side observes the closed link via EOF.
	_ = serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := proto.NewReader(serverConn).ReadMsg(); err == nil {
		t.Fatal("expected link to be closed after DetachWindow")
	}
	_ = context.Background()
}
