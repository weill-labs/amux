package test

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// paneOutputRecorder attaches a second client to a test session and records
// raw MsgTypePaneOutput bytes by pane ID.
type paneOutputRecorder struct {
	conn      net.Conn
	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	updates   chan struct{}

	mu       sync.Mutex
	paneData map[uint32][]byte
	readErr  error
}

func newPaneOutputRecorder(tb testing.TB, sockPath, session string, cols, rows int) *paneOutputRecorder {
	tb.Helper()

	conn, err := dialHeadlessSocket(sockPath, 3*time.Second)
	if err != nil {
		tb.Fatalf("dialing pane output recorder: %v", err)
	}

	caps := proto.KnownClientCapabilities()
	if err := server.WriteMsg(conn, &server.Message{
		Type:               server.MsgTypeAttach,
		Session:            session,
		Cols:               cols,
		Rows:               rows,
		AttachCapabilities: &caps,
	}); err != nil {
		_ = conn.Close()
		tb.Fatalf("attaching pane output recorder: %v", err)
	}

	r := &paneOutputRecorder{
		conn:     conn,
		ready:    make(chan struct{}),
		done:     make(chan struct{}),
		updates:  make(chan struct{}, 1),
		paneData: make(map[uint32][]byte),
	}
	go r.readLoop()

	select {
	case <-r.ready:
		return r
	case <-r.done:
		tb.Fatalf("pane output recorder closed before ready: %v", r.err())
	case <-time.After(10 * time.Second):
		r.close()
		tb.Fatal("timeout waiting for pane output recorder layout")
	}

	return nil
}

func (r *paneOutputRecorder) clearPane(paneID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.paneData, paneID)
}

func (r *paneOutputRecorder) waitForBytes(paneID uint32, want []byte, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if r.contains(paneID, want) {
			return true
		}
		if r.err() != nil {
			return r.contains(paneID, want)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}

		timer := time.NewTimer(remaining)
		select {
		case <-r.updates:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-r.done:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			return r.contains(paneID, want)
		}
	}
}

func (r *paneOutputRecorder) close() {
	if r == nil || r.conn == nil {
		return
	}
	_ = r.conn.Close()
	<-r.done
}

func (r *paneOutputRecorder) readLoop() {
	defer close(r.done)
	for {
		msg, err := server.ReadMsg(r.conn)
		if err != nil {
			r.setErr(err)
			r.readyOnce.Do(func() { close(r.ready) })
			return
		}
		switch msg.Type {
		case server.MsgTypeLayout:
			r.readyOnce.Do(func() { close(r.ready) })
		case server.MsgTypePaneOutput:
			r.mu.Lock()
			r.paneData[msg.PaneID] = append(r.paneData[msg.PaneID], msg.PaneData...)
			r.mu.Unlock()
			r.notifyUpdate()
		case server.MsgTypeExit:
			r.readyOnce.Do(func() { close(r.ready) })
			return
		}
	}
}

func (r *paneOutputRecorder) contains(paneID uint32, want []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.paneData[paneID], want)
}

func (r *paneOutputRecorder) err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.readErr
}

func (r *paneOutputRecorder) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readErr == nil {
		r.readErr = fmt.Errorf("reading pane output recorder: %w", err)
	}
}

func (r *paneOutputRecorder) notifyUpdate() {
	select {
	case r.updates <- struct{}{}:
	default:
	}
}
