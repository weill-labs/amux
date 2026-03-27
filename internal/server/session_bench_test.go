package server

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

type discardConn struct{}

func (discardConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (discardConn) Write(b []byte) (int, error)      { return len(b), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) LocalAddr() net.Addr              { return discardAddr("local") }
func (discardConn) RemoteAddr() net.Addr             { return discardAddr("remote") }
func (discardConn) SetDeadline(time.Time) error      { return nil }
func (discardConn) SetReadDeadline(time.Time) error  { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }

type discardAddr string

func (a discardAddr) Network() string { return "discard" }
func (a discardAddr) String() string  { return string(a) }

func benchProxyPane(id uint32) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  fmt.Sprintf("pane-%d", id),
		Host:  mux.DefaultHost,
		Color: config.AccentColor(id - 1),
	}, 80, 199, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
}

func benchSessionWithPanes(n int) *Session {
	panes := make([]*mux.Pane, 0, n)
	first := benchProxyPane(1)
	panes = append(panes, first)

	w := mux.NewWindow(first, 80, 200)
	w.ID = 1
	w.Name = "window-1"

	for i := 2; i <= n; i++ {
		pane := benchProxyPane(uint32(i))
		panes = append(panes, pane)
		if _, err := w.SplitRoot(mux.SplitHorizontal, pane); err != nil {
			panic(err)
		}
	}

	return &Session{
		Name:           "bench",
		Windows:        []*mux.Window{w},
		ActiveWindowID: w.ID,
		Panes:          panes,
		idle:           newIdleTracker(),
		waiters:        newWaiterManager(),
	}
}

// benchBroadcastLayoutNow measures stable broadcast work without depending on
// async client-writer queue depth or dropped-frame policy.
func (s *Session) benchBroadcastLayoutNow(w io.Writer) error {
	idleSnap := s.snapshotIdleState()
	s.assertPaneLayoutConsistency()
	snap := s.snapshotLayout(idleSnap)
	if snap == nil {
		return nil
	}

	gen := s.generation.Add(1)
	s.notifyLayoutWaiters(gen)

	if err := WriteMsg(w, &Message{Type: MsgTypeLayout, Layout: snap}); err != nil {
		return err
	}

	activePaneName := ""
	if snap.ActivePaneID != 0 {
		for _, p := range snap.Panes {
			if p.ID == snap.ActivePaneID {
				activePaneName = p.Name
				break
			}
		}
	}
	s.emitEvent(Event{Type: EventLayout, Generation: gen, ActivePane: activePaneName})

	select {
	case s.crashCheckpointTrigger <- struct{}{}:
	default:
	}

	return nil
}

func BenchmarkSessionSnapshotLayout(b *testing.B) {
	for _, panes := range []int{1, 4, 20} {
		b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
			sess := benchSessionWithPanes(panes)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if snap := sess.snapshotLayout(nil); snap == nil {
					b.Fatal("snapshotLayout returned nil")
				}
			}
		})
	}
}

func BenchmarkSessionBroadcastLayout(b *testing.B) {
	for _, panes := range []int{1, 4, 20} {
		b.Run(fmt.Sprintf("panes_%d", panes), func(b *testing.B) {
			sess := benchSessionWithPanes(panes)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := sess.benchBroadcastLayoutNow(io.Discard); err != nil {
					b.Fatalf("benchBroadcastLayoutNow: %v", err)
				}
			}
		})
	}
}
