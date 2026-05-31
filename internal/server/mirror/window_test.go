package mirror

import (
	"context"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

// TestManagerWindowMirrorRetryBudgetEndsDead drives the window-mirror connect
// retry loop to exhaustion against a failing dialer, covering the retry/error
// state machine (runWindow, attachAndReadWindow, prepareWindowAttempt,
// recordWindowError, markWindowDead).
func TestManagerWindowMirrorRetryBudgetEndsDead(t *testing.T) {
	t.Parallel()

	mgr := NewManager(Config{
		Hosts:       map[string]config.Host{"remote": {SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"}},
		Dialer:      failingDialer{err: errors.New("dial failed")},
		RetryPolicy: remoteRetryPolicyForTest(2),
	})
	t.Cleanup(mgr.Close)

	if err := mgr.TrackWindow(7, WindowRef{Host: "remote", Session: "main", WindowName: "amux"}, 80, 24); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}

	waitForWindowMirrorState(t, mgr, 7, StateDead)
	snap, ok := mgr.WindowSnapshot(7)
	if !ok || snap.LastError == "" {
		t.Fatalf("expected dead window mirror with an error, got %+v ok=%v", snap, ok)
	}
}

// waitForWindowMirrorState polls the window mirror state, yielding the processor
// (runtime.Gosched) between checks rather than blocking, so no sleep is needed.
func waitForWindowMirrorState(t *testing.T, mgr *Manager, id uint32, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snap, ok := mgr.WindowSnapshot(id); ok && snap.State == want {
			return
		}
		runtime.Gosched()
	}
	snap, _ := mgr.WindowSnapshot(id)
	t.Fatalf("window mirror state = %s, want %s (last error %q)", snap.State, want, snap.LastError)
}

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

	if err := mgr.TrackWindow(7, WindowRef{Host: "h", Session: "main", WindowName: "amux"}, 80, 24); err != nil {
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
	if sub.Cols != 80 || sub.Rows != 24 {
		t.Fatalf("subscription size = %dx%d, want 80x24", sub.Cols, sub.Rows)
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

func TestManagerResizeWindowPushesSize(t *testing.T) {
	t.Parallel()

	connected := make(chan struct{}, 1)
	dialer := &pipeDialer{conns: make(chan net.Conn, 1)}
	mgr := NewManager(Config{
		Hosts:       map[string]config.Host{"h": {SSH: "x", Session: "main", SocketPath: "/tmp/x"}},
		Dialer:      dialer,
		RetryPolicy: remoteRetryPolicyForTest(2),
		OnWindowLayout: func(uint32, WindowRef, *proto.LayoutSnapshot) {
			select {
			case connected <- struct{}{}:
			default:
			}
		},
	})
	t.Cleanup(mgr.Close)

	if err := mgr.TrackWindow(7, WindowRef{Host: "h", Session: "main", WindowName: "amux"}, 80, 24); err != nil {
		t.Fatalf("TrackWindow: %v", err)
	}
	serverConn := <-dialer.conns
	t.Cleanup(func() { _ = serverConn.Close() })

	reader := proto.NewReader(serverConn)
	if _, err := reader.ReadMsg(); err != nil { // drain the AttachWindow subscription
		t.Fatalf("read subscription: %v", err)
	}

	// Pushing a layout proves the mirror is Connected (layouts are delivered only
	// after markWindowConnected), so the subsequent ResizeWindow will send.
	if err := proto.NewWriter(serverConn).WriteMsg(&proto.Message{
		Type:   proto.MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{Windows: []proto.WindowSnapshot{{Name: "amux"}}},
	}); err != nil {
		t.Fatalf("write layout: %v", err)
	}
	<-connected

	// ResizeWindow is non-blocking (the write happens on a drainer goroutine),
	// so the read below drains the synchronous net.Pipe.
	mgr.ResizeWindow(7, 120, 30)

	resize, err := reader.ReadMsg()
	if err != nil {
		t.Fatalf("read resize: %v", err)
	}
	if resize.Type != proto.MsgTypeResize || resize.Cols != 120 || resize.Rows != 30 {
		t.Fatalf("resize = type %d %dx%d, want MsgTypeResize 120x30", resize.Type, resize.Cols, resize.Rows)
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
			if err := mgr.TrackWindow(tt.id, tt.ref, 80, 24); err == nil {
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

	if err := mgr.TrackWindow(7, WindowRef{Host: "h", Session: "main", WindowName: "amux"}, 80, 24); err != nil {
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
