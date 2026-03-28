package server

import (
	"log"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

// recalcSize resizes all windows to the latest active client's terminal size,
// matching tmux's "window-size latest" behavior. Event-loop only.
func (s *Session) recalcSize() {
	sizeClient := s.effectiveSizeClient()
	if sizeClient == nil || sizeClient.cols == 0 || sizeClient.rows == 0 {
		return
	}
	layoutH := sizeClient.rows - render.GlobalBarHeight
	if aw := s.activeWindow(); aw != nil && sizeClient.cols == aw.Width && layoutH == aw.Height {
		return
	}
	for _, w := range s.Windows {
		w.Resize(sizeClient.cols, layoutH)
	}
}

func (s *Session) broadcastNow(msg *Message) {
	clients := s.ensureClientManager().snapshotClients()
	for _, c := range clients {
		c.sendBroadcast(msg)
	}
}

// broadcast sends a message to all connected clients.
func (s *Session) broadcast(msg *Message) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.broadcastNow(msg)
		return struct{}{}, nil
	})
}

// clipboardCallback returns the onClipboard callback for panes in this session.
// It forwards OSC 52 clipboard sequences to all connected clients and
// increments the clipboard generation counter for wait-clipboard.
func (s *Session) clipboardCallback() func(paneID uint32, data []byte) {
	return func(paneID uint32, data []byte) {
		if s.shutdown.Load() {
			return
		}
		s.enqueueClipboard(paneID, data)
	}
}

// metaCallback returns the onMetaUpdate callback for panes in this session.
func (s *Session) metaCallback() func(paneID uint32, update mux.MetaUpdate) {
	return func(paneID uint32, update mux.MetaUpdate) {
		if s.shutdown.Load() {
			return
		}
		s.enqueueEvent(metaUpdateEvent{paneID: paneID, update: update})
	}
}

func (s *Session) broadcastPaneOutputNow(paneID uint32, data []byte, seq uint64) {
	clients := s.ensureClientManager().snapshotClients()
	msg := &Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data}
	for _, c := range clients {
		if seq == 0 {
			c.sendPaneMessage(msg)
			continue
		}
		c.sendPaneOutput(msg, paneID, seq)
	}
	s.notifyPaneOutputSubs(paneID)
	s.trackPaneVTIdle(paneID)
	s.trackPaneActivity(paneID)

	var paneName, host string
	if p := s.findPaneByID(paneID); p != nil {
		paneName = p.Meta.Name
		host = p.Meta.Host
		state := s.capturePaneTerminalState(p)
		prev, ok := s.terminalEventState[paneID]
		if !ok || !paneTerminalEventStateEqual(prev, state) {
			s.terminalEventState[paneID] = state
			s.emitEvent(Event{
				Type:     EventTerminal,
				PaneID:   paneID,
				PaneName: paneName,
				Host:     host,
				Cursor:   &state.Cursor,
				Terminal: state.Terminal,
			})
		}
	}
	s.emitEvent(Event{Type: EventOutput, PaneID: paneID, PaneName: paneName, Host: host})

	select {
	case s.crashCheckpointTrigger <- struct{}{}:
	default:
	}
}

func (s *Session) broadcastPaneHistoryNow(paneID uint32, history []string) {
	clients := s.ensureClientManager().snapshotClients()
	msg := &Message{Type: MsgTypePaneHistory, PaneID: paneID, History: append([]string(nil), history...)}
	for _, c := range clients {
		c.sendPaneMessage(msg)
	}

	select {
	case s.crashCheckpointTrigger <- struct{}{}:
	default:
	}
}

// broadcastPaneOutput sends raw PTY output for one pane to all clients,
// notifies any wait-for subscribers, and tracks pane activity.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte, seq uint64) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.broadcastPaneOutputNow(paneID, data, seq)
		return struct{}{}, nil
	})
}

func (s *Session) notifyLayoutWaiters(gen uint64) {
	s.ensureWaiters().notifyLayoutWaiters(gen)
}

func (s *Session) notifyClipboardWaiters(gen uint64, payload string) {
	s.ensureWaiters().notifyClipboardWaiters(gen, payload)
}

func (s *Session) broadcastLayoutNow() {
	idleSnap := s.snapshotIdleState()
	s.assertPaneLayoutConsistency()
	snap := s.snapshotLayout(idleSnap)
	if snap == nil {
		return
	}

	gen := s.generation.Add(1)
	s.notifyLayoutWaiters(gen)

	s.broadcastNow(&Message{Type: MsgTypeLayout, Layout: snap})

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
}

// broadcastLayout sends the current layout snapshot to all clients
// and increments the layout generation counter.
func (s *Session) broadcastLayout() {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.broadcastLayoutNow()
		return struct{}{}, nil
	})
}

// snapshotIdleState returns a copy of the session's idle state map.
func (s *Session) snapshotIdleState() map[uint32]bool {
	return s.idle.SnapshotState()
}

// snapshotIdleFull returns copies of both idle state and since maps.
func (s *Session) snapshotIdleFull() (map[uint32]bool, map[uint32]time.Time) {
	return s.idle.SnapshotFull()
}

// snapshotLayout builds a LayoutSnapshot with multi-window data.
func (s *Session) snapshotLayout(idleSnap map[uint32]bool) *proto.LayoutSnapshot {
	w := s.activeWindow()
	if w == nil {
		return nil
	}

	snap := w.SnapshotLayout(s.Name)
	snap.ActiveWindowID = s.ActiveWindowID
	snap.Notice = s.notice

	for i, win := range s.Windows {
		snap.Windows = append(snap.Windows, win.SnapshotWindow(i+1))
	}

	for i := range snap.Panes {
		snap.Panes[i].Idle = idleSnap[snap.Panes[i].ID]
	}
	for wi := range snap.Windows {
		for pi := range snap.Windows[wi].Panes {
			snap.Windows[wi].Panes[pi].Idle = idleSnap[snap.Windows[wi].Panes[pi].ID]
		}
	}

	return snap
}

// assertPaneLayoutConsistency checks that every non-dormant pane in the flat
// registry exists in some window's layout tree. Logs a warning for each
// violation — these indicate the dual data-structure divergence that causes
// ghost panes (LAB-210).
func (s *Session) assertPaneLayoutConsistency() int {
	n := 0
	for _, p := range s.Panes {
		if p.Meta.Dormant {
			continue
		}
		if s.findWindowByPaneID(p.ID) == nil {
			log.Printf("[amux] consistency warning: pane %d (%s) is non-dormant but not in any window layout", p.ID, p.Meta.Name)
			n++
		}
	}
	return n
}

// notifyPaneOutputSubs wakes all wait-for subscribers for the given pane.
// Only called from the event loop (paneOutputEvent); no mutex needed.
func (s *Session) notifyPaneOutputSubs(paneID uint32) {
	s.ensureWaiters().notifyPaneOutputSubs(paneID)
}

func (s *Session) trackPaneVTIdle(paneID uint32) {
	if s.vtIdle == nil {
		return
	}
	s.vtIdle.TrackOutput(paneID, s.vtIdleSettle(), func(lastOutput time.Time) {
		s.enqueueVTIdleTimeout(paneID, lastOutput)
	})
}

// trackPaneActivity is called on every PTY output. It resets the idle timer.
// When the idle state transitions (idle↔busy), a layout broadcast is sent so clients see the
// updated PaneSnapshot.Idle (used for idle indicators in the status bar).
func (s *Session) trackPaneActivity(paneID uint32) {
	wasIdle := s.idle.TrackActivity(paneID, DefaultIdleTimeout, func() {
		s.enqueueIdleTimeout(paneID)
	})

	if wasIdle {
		pane := s.findPaneByID(paneID)
		paneName, host := "", ""
		if pane != nil {
			paneName = pane.Meta.Name
			host = pane.Meta.Host
		}
		s.emitEvent(Event{
			Type:     EventBusy,
			PaneID:   paneID,
			PaneName: paneName,
			Host:     host,
		})
		s.broadcastLayoutNow()
	}
}

// paneScreenContains checks whether the screen of the given pane contains
// the substring. It resolves the pane through the session event loop, then
// inspects the emulator outside the event loop.
func (s *Session) paneScreenContains(paneID uint32, substr string) bool {
	pane, err := enqueueSessionQuery(s, func(s *Session) (*mux.Pane, error) {
		return s.findPaneByID(paneID), nil
	})
	if err != nil || pane == nil {
		return false
	}
	return pane.ScreenContains(substr)
}

func (s *Session) addPaneOutputSubscriber(paneID uint32) chan struct{} {
	return s.ensureWaiters().addPaneOutputSubscriber(paneID)
}

// beginPaneOutputWait atomically registers a pane-output subscriber and checks
// the pane screen for the target substring inside the session actor. This
// avoids the lost-wakeup race where output lands between an initial
// ScreenContains check and later subscription registration.
func (s *Session) beginPaneOutputWait(paneID uint32, substr string) (paneOutputWaitStart, error) {
	return s.ensureWaiters().beginPaneOutputWait(s, paneID, substr)
}

// waitGeneration blocks until the layout generation exceeds afterGen or
// timeout expires. Returns the current generation and whether it matched.
func (s *Session) waitGeneration(afterGen uint64, timeout time.Duration) (uint64, bool) {
	return s.ensureWaiters().waitGeneration(s, afterGen, timeout)
}

func (s *Session) waitGenerationAfterCurrent(timeout time.Duration) (uint64, bool) {
	return s.ensureWaiters().waitGenerationAfterCurrent(s, timeout)
}

// waitClipboard blocks until the clipboard generation exceeds afterGen or
// timeout expires. Returns the last clipboard payload and whether it matched.
func (s *Session) waitClipboard(afterGen uint64, timeout time.Duration) (string, bool) {
	return s.ensureWaiters().waitClipboard(s, afterGen, timeout)
}

func (s *Session) waitClipboardAfterCurrent(timeout time.Duration) (string, bool) {
	return s.ensureWaiters().waitClipboardAfterCurrent(s, timeout)
}

func (s *Session) waitCrashCheckpoint(afterGen uint64, timeout time.Duration) (crashCheckpointRecord, bool) {
	return s.ensureWaiters().waitCrashCheckpoint(s, afterGen, timeout)
}

func (s *Session) waitCrashCheckpointAfterCurrent(timeout time.Duration) (crashCheckpointRecord, bool) {
	return s.ensureWaiters().waitCrashCheckpointAfterCurrent(s, timeout)
}
