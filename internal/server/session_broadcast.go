package server

import (
	"fmt"
	"log"
	"time"

	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/proto"
)

// broadcast sends a message to all connected clients.
func (s *Session) broadcast(msg *Message) {
	s.mu.Lock()
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	for _, c := range clients {
		c.Send(msg)
	}
}

// clipboardCallback returns the onClipboard callback for panes in this session.
// It forwards OSC 52 clipboard sequences to all connected clients and
// increments the clipboard generation counter for wait-clipboard.
func (s *Session) clipboardCallback() func(paneID uint32, data []byte) {
	return func(paneID uint32, data []byte) {
		if s.shutdown.Load() {
			return
		}
		s.broadcast(&Message{Type: MsgTypeClipboard, PaneID: paneID, PaneData: data})

		s.clipboardMu.Lock()
		s.lastClipboardB64 = string(data)
		s.clipboardGen.Add(1)
		s.clipboardCond.Broadcast()
		s.clipboardMu.Unlock()
	}
}

// broadcastPaneOutput sends raw PTY output for one pane to all clients,
// notifies any wait-for subscribers, and tracks pane activity for hooks.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte) {
	s.broadcast(&Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data})
	s.notifyPaneOutputSubs(paneID)
	s.trackPaneActivity(paneID)

	// Emit output event for event stream subscribers.
	s.mu.Lock()
	var paneName, host string
	if p := s.findPaneLocked(paneID); p != nil {
		paneName = p.Meta.Name
		host = p.Meta.Host
	}
	s.mu.Unlock()
	s.events.Emit(Event{Type: EventOutput, PaneID: paneID, PaneName: paneName, Host: host})
}

// broadcastPaneOutputLocked sends raw PTY output to all clients.
// Caller must hold s.mu.
func (s *Session) broadcastPaneOutputLocked(paneID uint32, data []byte) {
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	msg := &Message{Type: MsgTypePaneOutput, PaneID: paneID, PaneData: data}
	for _, c := range clients {
		c.Send(msg)
	}
}

// broadcastLayout sends the current layout snapshot to all clients
// and increments the layout generation counter.
func (s *Session) broadcastLayout() {
	idleSnap := s.snapshotIdleState()
	s.mu.Lock()
	s.assertPaneLayoutConsistency()
	snap := s.snapshotLayoutLocked(idleSnap)
	if snap == nil {
		s.mu.Unlock()
		return
	}
	clients := make([]*ClientConn, len(s.clients))
	copy(clients, s.clients)
	s.mu.Unlock()

	// Increment generation and wake any wait-layout waiters.
	s.generationMu.Lock()
	gen := s.generation.Add(1)
	s.generationCond.Broadcast()
	s.generationMu.Unlock()

	msg := &Message{Type: MsgTypeLayout, Layout: snap}
	for _, c := range clients {
		c.Send(msg)
	}

	// Emit layout event for event stream subscribers.
	activePaneName := ""
	if snap.ActivePaneID != 0 {
		for _, p := range snap.Panes {
			if p.ID == snap.ActivePaneID {
				activePaneName = p.Name
				break
			}
		}
	}
	s.events.Emit(Event{Type: EventLayout, Generation: gen, ActivePane: activePaneName})

	// Signal crash checkpoint loop (non-blocking — drop if already pending)
	select {
	case s.crashCheckpointTrigger <- struct{}{}:
	default:
	}
}

// snapshotIdleState returns a copy of the session's idle state map.
// Must be called before acquiring s.mu to maintain lock ordering:
// trackPaneActivity holds idle.mu then acquires s.mu (via buildPaneEnv),
// so callers must acquire idle.mu before s.mu.
func (s *Session) snapshotIdleState() map[uint32]bool {
	return s.idle.SnapshotState()
}

// snapshotIdleFull returns copies of both idle state and since maps.
// Same lock ordering requirement as snapshotIdleState.
func (s *Session) snapshotIdleFull() (map[uint32]bool, map[uint32]time.Time) {
	return s.idle.SnapshotFull()
}

// snapshotLayoutLocked builds a LayoutSnapshot with multi-window data.
// Caller must hold s.mu.
func (s *Session) snapshotLayoutLocked(idleSnap map[uint32]bool) *proto.LayoutSnapshot {
	w := s.ActiveWindow()
	if w == nil {
		return nil
	}

	// Build legacy single-window fields for the active window
	snap := w.SnapshotLayout(s.Name)
	snap.ActiveWindowID = s.ActiveWindowID

	// Build multi-window snapshots
	for i, win := range s.Windows {
		snap.Windows = append(snap.Windows, win.SnapshotWindow(i+1))
	}

	// Stamp idle state from the pre-acquired snapshot.
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
// ghost panes (LAB-210). Caller must hold s.mu.
func (s *Session) assertPaneLayoutConsistency() int {
	n := 0
	for _, p := range s.Panes {
		if p.Meta.Dormant {
			continue
		}
		if s.FindWindowByPaneID(p.ID) == nil {
			log.Printf("[amux] consistency warning: pane %d (%s) is non-dormant but not in any window layout", p.ID, p.Meta.Name)
			n++
		}
	}
	return n
}

// subscribePaneOutput registers a channel to receive notifications when
// PTY output arrives for the given pane. Returns the channel.
func (s *Session) subscribePaneOutput(paneID uint32) chan struct{} {
	ch := make(chan struct{}, 1)
	s.paneOutputMu.Lock()
	if s.paneOutputSubs == nil {
		s.paneOutputSubs = make(map[uint32][]chan struct{})
	}
	s.paneOutputSubs[paneID] = append(s.paneOutputSubs[paneID], ch)
	s.paneOutputMu.Unlock()
	return ch
}

// unsubscribePaneOutput removes a previously registered subscriber channel.
func (s *Session) unsubscribePaneOutput(paneID uint32, ch chan struct{}) {
	s.paneOutputMu.Lock()
	subs := s.paneOutputSubs[paneID]
	for i, sub := range subs {
		if sub == ch {
			s.paneOutputSubs[paneID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	s.paneOutputMu.Unlock()
}

// notifyPaneOutputSubs wakes all wait-for subscribers for the given pane.
func (s *Session) notifyPaneOutputSubs(paneID uint32) {
	s.paneOutputMu.Lock()
	subs := make([]chan struct{}, len(s.paneOutputSubs[paneID]))
	copy(subs, s.paneOutputSubs[paneID])
	s.paneOutputMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// trackPaneActivity is called on every PTY output. It resets the idle timer
// and fires on-activity if the pane was previously idle. When the idle state
// transitions (idle↔busy), a layout broadcast is sent so clients see the
// updated PaneSnapshot.Idle (used for idle indicators in the status bar).
func (s *Session) trackPaneActivity(paneID uint32) {
	wasIdle := s.idle.TrackActivity(paneID, DefaultIdleTimeout, func() {
		s.idle.MarkIdle(paneID)
		env := s.buildPaneEnv(paneID, hooks.OnIdle)
		s.Hooks.Fire(hooks.OnIdle, env)
		s.events.Emit(Event{
			Type:     EventIdle,
			PaneID:   paneID,
			PaneName: env["AMUX_PANE_NAME"],
			Host:     env["AMUX_HOST"],
		})
		s.broadcastLayout()
	})

	if wasIdle {
		env := s.buildPaneEnv(paneID, hooks.OnActivity)
		s.Hooks.Fire(hooks.OnActivity, env)
		s.events.Emit(Event{
			Type:     EventBusy,
			PaneID:   paneID,
			PaneName: env["AMUX_PANE_NAME"],
			Host:     env["AMUX_HOST"],
		})
		s.broadcastLayout()
	}
}

// buildPaneEnv builds the environment variable map for a hook invocation.
// Acquires s.mu internally to look up pane metadata.
func (s *Session) buildPaneEnv(paneID uint32, event hooks.Event) map[string]string {
	env := map[string]string{
		"AMUX_PANE_ID": fmt.Sprintf("%d", paneID),
		"AMUX_EVENT":   string(event),
	}

	s.mu.Lock()
	if p := s.findPaneLocked(paneID); p != nil {
		env["AMUX_PANE_NAME"] = p.Meta.Name
		if p.Meta.Task != "" {
			env["AMUX_TASK"] = p.Meta.Task
		}
		if p.Meta.Host != "" {
			env["AMUX_HOST"] = p.Meta.Host
		}
	}
	s.mu.Unlock()

	return env
}

// paneScreenContains checks whether the screen of the given pane contains
// the substring. Thread-safe: looks up the pane under s.mu, then reads the
// cell grid directly (no ANSI round-trip) outside the lock.
func (s *Session) paneScreenContains(paneID uint32, substr string) bool {
	s.mu.Lock()
	pane := s.findPaneLocked(paneID)
	s.mu.Unlock()
	if pane == nil {
		return false
	}
	return pane.ScreenContains(substr)
}

// waitGeneration blocks until the layout generation exceeds afterGen or
// timeout expires. Returns the current generation and whether it matched.
// All checks happen under generationMu to avoid TOCTOU races with Broadcast.
func (s *Session) waitGeneration(afterGen uint64, timeout time.Duration) (uint64, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		s.generationMu.Lock()
		s.generationCond.Broadcast()
		s.generationMu.Unlock()
	})
	defer timer.Stop()

	s.generationMu.Lock()
	defer s.generationMu.Unlock()
	for {
		gen := s.generation.Load()
		if gen > afterGen {
			return gen, true
		}
		if time.Now().After(deadline) {
			return gen, false
		}
		s.generationCond.Wait()
	}
}

// waitClipboard blocks until the clipboard generation exceeds afterGen or
// timeout expires. Returns the last clipboard payload and whether it matched.
func (s *Session) waitClipboard(afterGen uint64, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() {
		s.clipboardMu.Lock()
		s.clipboardCond.Broadcast()
		s.clipboardMu.Unlock()
	})
	defer timer.Stop()

	s.clipboardMu.Lock()
	defer s.clipboardMu.Unlock()
	for {
		gen := s.clipboardGen.Load()
		if gen > afterGen {
			return s.lastClipboardB64, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		s.clipboardCond.Wait()
	}
}
