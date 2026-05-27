package server

import (
	"context"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

type paneOutputEvent struct {
	paneID uint32
	data   []byte
	seq    uint64
}

func (e paneOutputEvent) handle(_ context.Context, s *Session) {
	s.broadcastPaneOutputNow(e.paneID, e.data, e.seq)
}

type paneExitEvent struct {
	paneID uint32
	reason string
}

func (e paneExitEvent) handle(_ context.Context, s *Session) {
	if s.shutdown.Load() {
		return
	}
	if s.ensureUndoManager().handlePaneExit(e.paneID, s.closePaneAsync) {
		return
	}
	s.handleFinalizedPaneRemoval(e.paneID, false, e.reason)
}

func (s *Session) handleFinalizedPaneRemoval(paneID uint32, closePane bool, reason string) {
	removed := s.finalizePaneRemoval(paneID)
	if removed.pane == nil {
		return
	}
	s.appendPaneLog(paneLogEventExit, removed.pane, reason)
	s.emitEvent(Event{
		Type:     EventPaneExit,
		PaneID:   paneID,
		PaneName: removed.paneName,
		Host:     removed.pane.Meta.Host,
		Reason:   reason,
	})
	s.logPaneExit(removed.pane, reason)
	if closePane {
		s.closePaneAsync(removed.pane)
	}
	if removed.sendExit {
		s.broadcastNow(&Message{Type: MsgTypeExit, Text: "session exited"})
		s.closeAllScopedPaneClients()
		s.wantShutdown = true
		return
	}
	s.closeScopedPaneClients(paneID, &Message{Type: MsgTypeExit, Text: "pane exited"})
	if removed.broadcastLayout {
		s.broadcastLayoutNow()
	}
}

func (s *Session) closeScopedPaneClients(paneID uint32, exitMsg *Message) {
	for _, cc := range s.ensureClientManager().snapshotClients() {
		if !cc.isPaneScoped() || !cc.isScopedToPane(paneID) {
			continue
		}
		s.closeScopedPaneClient(cc, exitMsg)
	}
}

func (s *Session) closeAllScopedPaneClients() {
	for _, cc := range s.ensureClientManager().snapshotClients() {
		if !cc.isPaneScoped() {
			continue
		}
		s.closeScopedPaneClient(cc, nil)
	}
}

func (s *Session) closeScopedPaneClient(cc *clientConn, exitMsg *Message) {
	cc.markDisconnectReason("pane exited")
	go func() {
		if exitMsg != nil {
			_ = cc.Send(exitMsg)
		}
		_ = cc.Flush()
		cc.Close()
	}()
}

type clipboardEvent struct {
	paneID uint32
	data   []byte
}

func (e clipboardEvent) handle(_ context.Context, s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.ensureWaiters().recordClipboard(e.data)
	s.broadcastNow(&Message{Type: MsgTypeClipboard, PaneID: e.paneID, PaneData: e.data})
}

type crashCheckpointWrittenEvent struct {
	path string
}

func (e crashCheckpointWrittenEvent) handle(_ context.Context, s *Session) {
	if e.path == "" {
		return
	}
	s.ensureWaiters().recordCrashCheckpoint(e.path)
}

type idleTimeoutEvent struct {
	paneID uint32
}

func (e idleTimeoutEvent) handle(_ context.Context, s *Session) {
	s.ensureIdleTracker().HandleIdleTimeout(e.paneID)

	// Refresh CWD/branch off the event loop to avoid blocking on lsof/git.
	// Tests can disable this background path to keep integration timing
	// deterministic when they need stable snapshots.
	if p := s.findPaneByID(e.paneID); p != nil && !p.IsProxy() && !s.DisablePaneMetaAutoRefresh {
		pane := p
		go func() {
			cwd, branch := s.detectPaneCwdBranch(pane)
			s.enqueueEvent(s.context(), cwdBranchResultEvent{paneID: e.paneID, cwd: cwd, branch: branch})
		}()
	}

	pane := s.findPaneByID(e.paneID)
	paneName, host := "", ""
	if pane != nil {
		paneName = pane.Meta.Name
		host = pane.Meta.Host
	}
	s.emitEvent(Event{
		Type:     EventIdle,
		PaneID:   e.paneID,
		PaneName: paneName,
		Host:     host,
	})
	if pane != nil && pane.ForegroundJobState().Idle {
		s.emitEvent(Event{
			Type:     EventExited,
			PaneID:   e.paneID,
			PaneName: paneName,
			Host:     host,
		})
	}
	s.broadcastLayoutNow()
}

type vtIdleTimeoutEvent struct {
	paneID     uint32
	lastOutput time.Time
}

func (e vtIdleTimeoutEvent) handle(_ context.Context, s *Session) {
	if !s.ensureIdleTracker().HandleVTIdleTimeout(e.paneID, e.lastOutput) {
		return
	}
}

type agentIdleCheckResultEvent struct {
	paneID   uint32
	paneName string
	host     string
	idle     bool
}

func (e agentIdleCheckResultEvent) handle(_ context.Context, s *Session) {
	if !e.idle {
		return
	}
	s.emitEvent(Event{
		Type:     EventExited,
		PaneID:   e.paneID,
		PaneName: e.paneName,
		Host:     e.host,
	})
}

type cwdBranchResultEvent struct {
	paneID uint32
	cwd    string
	branch string
}

func (e cwdBranchResultEvent) handle(_ context.Context, s *Session) {
	if p := s.findPaneByID(e.paneID); p != nil {
		p.ApplyCwdBranch(e.cwd, e.branch)
		s.broadcastLayoutNow()
	}
}

type metaUpdateEvent struct {
	paneID uint32
	update mux.MetaUpdate
}

func (e metaUpdateEvent) handle(_ context.Context, s *Session) {
	p := s.findPaneByID(e.paneID)
	if p == nil {
		return
	}
	if e.update.Task != nil {
		_ = setPaneKVValue(p, mux.PaneMetaKeyTask, *e.update.Task)
	}
	if e.update.PR != nil {
		_ = setPaneKVValue(p, mux.PaneMetaKeyPR, *e.update.PR)
	}
	if e.update.Branch != nil {
		_ = setPaneKVValue(p, mux.PaneMetaKeyBranch, *e.update.Branch)
	}
	s.broadcastLayoutNow()
}

type localPaneBuildResultEvent struct {
	placeholder *mux.Pane
	pane        *mux.Pane
	err         error
	done        chan error
}

func (e localPaneBuildResultEvent) handle(_ context.Context, s *Session) {
	notify := func(err error) {
		if e.done == nil {
			return
		}
		e.done <- err
	}

	if e.placeholder == nil {
		if e.pane != nil {
			e.pane.SuppressCallbacks()
			s.closePaneAsync(e.pane)
		}
		notify(fmt.Errorf("missing pending pane"))
		return
	}

	current := s.findPaneByID(e.placeholder.ID)
	if current == nil {
		if e.pane != nil {
			e.pane.SuppressCallbacks()
			s.closePaneAsync(e.pane)
		}
		notify(e.err)
		return
	}

	if e.err != nil {
		s.failPendingLocalPaneBuild(current, e.err)
		notify(e.err)
		return
	}
	if e.pane == nil {
		err := fmt.Errorf("missing local pane runtime")
		s.failPendingLocalPaneBuild(current, err)
		notify(err)
		return
	}

	w := s.findWindowByPaneID(current.ID)
	if w == nil {
		e.pane.SuppressCallbacks()
		s.closePaneAsync(e.pane)
		err := fmt.Errorf("pane not in any window")
		s.failPendingLocalPaneBuild(current, err)
		notify(err)
		return
	}
	e.pane.Meta = clonePaneMetaForReplacement(current.Meta)
	e.pane.SetMetaManualBranch(current.MetaManualBranch())
	if err := s.replacePaneInstance(current, e.pane, w); err != nil {
		e.pane.SuppressCallbacks()
		s.closePaneAsync(e.pane)
		s.failPendingLocalPaneBuild(current, err)
		notify(err)
		return
	}

	s.ensureInputRouter().syncPanes(s.Panes)
	current.SuppressCallbacks()
	s.closePaneAsync(current)
	e.pane.Start()
	notify(nil)
}

func (s *Session) enqueuePaneOutput(paneID uint32, data []byte, seq uint64) {
	s.enqueueEvent(s.context(), paneOutputEvent{paneID: paneID, data: data, seq: seq})
}

func (s *Session) enqueuePaneExit(paneID uint32, reason string) {
	s.enqueueEvent(s.context(), paneExitEvent{paneID: paneID, reason: reason})
}

func (s *Session) enqueueClipboard(paneID uint32, data []byte) {
	s.enqueueEvent(s.context(), clipboardEvent{paneID: paneID, data: data})
}

func (s *Session) enqueueIdleTimeout(paneID uint32) {
	s.enqueueEvent(s.context(), idleTimeoutEvent{paneID: paneID})
}

func (s *Session) enqueueVTIdleTimeout(paneID uint32, lastOutput time.Time) {
	s.enqueueEvent(s.context(), vtIdleTimeoutEvent{paneID: paneID, lastOutput: lastOutput})
}

// --- Pane output subscribe/unsubscribe through the event loop ---

type paneOutputSubscribeCmd struct {
	paneID uint32
	reply  chan chan struct{}
}

func (e paneOutputSubscribeCmd) handle(ctx context.Context, s *Session) {
	ch := s.addPaneOutputSubscriber(e.paneID)
	select {
	case e.reply <- ch:
	case <-ctx.Done():
		s.ensureWaiters().removePaneOutputSubscriber(e.paneID, ch)
	}
}

type paneOutputUnsubscribeCmd struct {
	paneID uint32
	ch     chan struct{}
}

func (e paneOutputUnsubscribeCmd) handle(_ context.Context, s *Session) {
	s.ensureWaiters().removePaneOutputSubscriber(e.paneID, e.ch)
}

func (s *Session) enqueuePaneOutputSubscribe(ctx context.Context, paneID uint32) chan struct{} {
	reply := make(chan (chan struct{}))
	if !s.enqueueEvent(ctx, paneOutputSubscribeCmd{paneID: paneID, reply: reply}) {
		return nil
	}
	select {
	case ch := <-reply:
		return ch
	case <-ctx.Done():
		return nil
	case <-s.sessionEventDone:
		return nil
	}
}

func (s *Session) enqueuePaneOutputUnsubscribe(paneID uint32, ch chan struct{}) {
	s.enqueueEvent(s.context(), paneOutputUnsubscribeCmd{paneID: paneID, ch: ch})
}
