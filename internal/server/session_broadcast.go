package server

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type hookResultRecord struct {
	Generation uint64
	Event      string
	PaneID     uint32
	PaneName   string
	Host       string
	Command    string
	Success    bool
	Error      string
}

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
	clients := append([]*clientConn(nil), s.clients...)
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
	clients := append([]*clientConn(nil), s.clients...)
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
	}
	s.emitEvent(Event{Type: EventOutput, PaneID: paneID, PaneName: paneName, Host: host})

	select {
	case s.crashCheckpointTrigger <- struct{}{}:
	default:
	}
}

func (s *Session) broadcastPaneHistoryNow(paneID uint32, history []string) {
	clients := append([]*clientConn(nil), s.clients...)
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
// notifies any wait-for subscribers, and tracks pane activity for hooks.
func (s *Session) broadcastPaneOutput(paneID uint32, data []byte, seq uint64) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.broadcastPaneOutputNow(paneID, data, seq)
		return struct{}{}, nil
	})
}

func (s *Session) notifyLayoutWaiters(gen uint64) {
	for id, waiter := range s.layoutWaiters {
		if gen <= waiter.afterGen {
			continue
		}
		waiter.reply <- gen
		delete(s.layoutWaiters, id)
	}
}

func (s *Session) notifyClipboardWaiters(gen uint64, payload string) {
	for id, waiter := range s.clipboardWaiters {
		if gen <= waiter.afterGen {
			continue
		}
		waiter.reply <- payload
		delete(s.clipboardWaiters, id)
	}
}

func (s *Session) matchHookResult(afterGen uint64, eventName string, paneID uint32, paneName string) (hookResultRecord, bool) {
	for _, record := range s.hookResults {
		if record.Generation <= afterGen {
			continue
		}
		if eventName != "" && record.Event != eventName {
			continue
		}
		if paneID != 0 && record.PaneID != 0 && record.PaneID != paneID {
			continue
		}
		if paneName != "" && record.PaneName != paneName {
			continue
		}
		return record, true
	}
	return hookResultRecord{}, false
}

func (s *Session) notifyHookWaiters(record hookResultRecord) {
	for id, waiter := range s.hookWaiters {
		if record.Generation <= waiter.afterGen {
			continue
		}
		if waiter.eventName != "" && record.Event != waiter.eventName {
			continue
		}
		if waiter.paneID != 0 && record.PaneID != 0 && record.PaneID != waiter.paneID {
			continue
		}
		if waiter.paneName != "" && record.PaneName != waiter.paneName {
			continue
		}
		waiter.reply <- record
		delete(s.hookWaiters, id)
	}
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

func (s *Session) fireHooks(event hooks.Event, env map[string]string) {
	s.Hooks.FireWithCallback(event, env, func(result hooks.Result) {
		paneID, _ := strconv.ParseUint(env["AMUX_PANE_ID"], 10, 32)
		s.enqueueEvent(hookResultEvent{
			record: hookResultRecord{
				Event:    string(result.Event),
				PaneID:   uint32(paneID),
				PaneName: env["AMUX_PANE_NAME"],
				Host:     env["AMUX_HOST"],
				Command:  result.Command,
				Success:  result.Err == nil,
				Error:    errorString(result.Err),
			},
		})
	})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
	for _, ch := range s.paneOutputSubs[paneID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Session) trackPaneVTIdle(paneID uint32) {
	if s.vtIdle == nil {
		return
	}
	s.vtIdle.TrackOutput(paneID, defaultVTIdleSettle, func(lastOutput time.Time) {
		s.enqueueVTIdleTimeout(paneID, lastOutput)
	})
}

// trackPaneActivity is called on every PTY output. It resets the idle timer
// and fires on-activity if the pane was previously idle. When the idle state
// transitions (idle↔busy), a layout broadcast is sent so clients see the
// updated PaneSnapshot.Idle (used for idle indicators in the status bar).
func (s *Session) trackPaneActivity(paneID uint32) {
	wasIdle := s.idle.TrackActivity(paneID, DefaultIdleTimeout, func() {
		s.enqueueIdleTimeout(paneID)
	})

	if wasIdle {
		env := s.buildPaneEnv(paneID, hooks.OnActivity)
		s.fireHooks(hooks.OnActivity, env)
		s.emitEvent(Event{
			Type:     EventBusy,
			PaneID:   paneID,
			PaneName: env["AMUX_PANE_NAME"],
			Host:     env["AMUX_HOST"],
		})
		s.broadcastLayoutNow()
	}
}

// buildPaneEnv builds the environment variable map for a hook invocation.
func (s *Session) buildPaneEnv(paneID uint32, event hooks.Event) map[string]string {
	env := map[string]string{
		"AMUX_PANE_ID": fmt.Sprintf("%d", paneID),
		"AMUX_EVENT":   string(event),
	}

	if p := s.findPaneByID(paneID); p != nil {
		env["AMUX_PANE_NAME"] = p.Meta.Name
		if p.Meta.Task != "" {
			env["AMUX_TASK"] = p.Meta.Task
		}
		if p.Meta.Host != "" {
			env["AMUX_HOST"] = p.Meta.Host
		}
	}

	return env
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

// waitGeneration blocks until the layout generation exceeds afterGen or
// timeout expires. Returns the current generation and whether it matched.
func (s *Session) waitGeneration(afterGen uint64, timeout time.Duration) (uint64, bool) {
	type waitRegistration struct {
		gen      uint64
		waiterID uint64
		reply    chan uint64
	}
	type waitState struct {
		gen     uint64
		matched bool
	}

	reg, err := enqueueSessionQuery(s, func(s *Session) (waitRegistration, error) {
		gen := s.generation.Load()
		if gen > afterGen {
			return waitRegistration{gen: gen}, nil
		}
		reply := make(chan uint64, 1)
		waiterID := s.waiterCounter.Add(1)
		s.layoutWaiters[waiterID] = layoutWaiter{afterGen: afterGen, reply: reply}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return 0, false
	}
	if reg.reply == nil {
		return reg.gen, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case gen := <-reg.reply:
		return gen, true
	case <-timer.C:
		state, err := enqueueSessionQuery(s, func(s *Session) (waitState, error) {
			delete(s.layoutWaiters, reg.waiterID)
			gen := s.generation.Load()
			return waitState{gen: gen, matched: gen > afterGen}, nil
		})
		if err != nil {
			return 0, false
		}
		return state.gen, state.matched
	}
}

// waitClipboard blocks until the clipboard generation exceeds afterGen or
// timeout expires. Returns the last clipboard payload and whether it matched.
func (s *Session) waitClipboard(afterGen uint64, timeout time.Duration) (string, bool) {
	type waitRegistration struct {
		payload  string
		waiterID uint64
		reply    chan string
	}
	type waitState struct {
		payload string
		matched bool
	}

	reg, err := enqueueSessionQuery(s, func(s *Session) (waitRegistration, error) {
		gen := s.clipboardGen.Load()
		if gen > afterGen {
			return waitRegistration{payload: s.lastClipboardB64}, nil
		}
		reply := make(chan string, 1)
		waiterID := s.waiterCounter.Add(1)
		s.clipboardWaiters[waiterID] = clipboardWaiter{afterGen: afterGen, reply: reply}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return "", false
	}
	if reg.reply == nil {
		return reg.payload, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case payload := <-reg.reply:
		return payload, true
	case <-timer.C:
		state, err := enqueueSessionQuery(s, func(s *Session) (waitState, error) {
			delete(s.clipboardWaiters, reg.waiterID)
			if s.clipboardGen.Load() > afterGen {
				return waitState{payload: s.lastClipboardB64, matched: true}, nil
			}
			return waitState{}, nil
		})
		if err != nil {
			return "", false
		}
		return state.payload, state.matched
	}
}

func (s *Session) waitHook(afterGen uint64, eventName, paneName string, timeout time.Duration) (hookResultRecord, bool) {
	return s.waitHookForPane(afterGen, eventName, 0, paneName, timeout)
}

func (s *Session) waitHookForPane(afterGen uint64, eventName string, paneID uint32, paneName string, timeout time.Duration) (hookResultRecord, bool) {
	type waitRegistration struct {
		record   hookResultRecord
		waiterID uint64
		reply    chan hookResultRecord
	}
	type waitState struct {
		record  hookResultRecord
		matched bool
	}

	reg, err := enqueueSessionQuery(s, func(s *Session) (waitRegistration, error) {
		if record, ok := s.matchHookResult(afterGen, eventName, paneID, paneName); ok {
			return waitRegistration{record: record}, nil
		}
		reply := make(chan hookResultRecord, 1)
		waiterID := s.waiterCounter.Add(1)
		s.hookWaiters[waiterID] = hookWaiter{
			afterGen:  afterGen,
			eventName: eventName,
			paneID:    paneID,
			paneName:  paneName,
			reply:     reply,
		}
		return waitRegistration{waiterID: waiterID, reply: reply}, nil
	})
	if err != nil {
		return hookResultRecord{}, false
	}
	if reg.reply == nil {
		return reg.record, true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case record := <-reg.reply:
		return record, true
	case <-timer.C:
		state, err := enqueueSessionQuery(s, func(s *Session) (waitState, error) {
			delete(s.hookWaiters, reg.waiterID)
			record, ok := s.matchHookResult(afterGen, eventName, paneID, paneName)
			return waitState{record: record, matched: ok}, nil
		})
		if err != nil {
			return hookResultRecord{}, false
		}
		return state.record, state.matched
	}
}
