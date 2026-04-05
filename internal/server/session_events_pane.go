package server

import (
	"fmt"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type paneOutputEvent struct {
	paneID uint32
	data   []byte
	seq    uint64
}

func (e paneOutputEvent) handle(s *Session) {
	s.broadcastPaneOutputNow(e.paneID, e.data, e.seq)
}

type paneExitEvent struct {
	paneID uint32
	reason string
}

func (e paneExitEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	// If the pane is in the undo stack (soft-closed), finalize it there
	// instead of going through the normal removal path.
	if pane := s.finalizeClosedPane(e.paneID); pane != nil {
		s.closePaneAsync(pane)
		return
	}
	s.handleFinalizedPaneRemoval(e.paneID, false, e.reason)
}

type paneCleanupTimeoutEvent struct {
	paneID uint32
}

func (e paneCleanupTimeoutEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	pane := s.findPaneByID(e.paneID)
	if pane == nil {
		return
	}
	_ = pane.SignalForegroundProcessGroup(syscall.SIGKILL)
	s.handleFinalizedPaneRemoval(e.paneID, true, "cleanup timeout")
}

type undoExpiryEvent struct {
	paneID uint32
}

func (e undoExpiryEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	pane := s.finalizeClosedPane(e.paneID)
	if pane != nil {
		s.closePaneAsync(pane)
	}
}

func (s *Session) enqueueUndoExpiry(paneID uint32) {
	s.enqueueEvent(undoExpiryEvent{paneID: paneID})
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
		s.broadcastNow(&Message{Type: MsgTypeExit})
		s.wantShutdown = true
		return
	}
	if removed.broadcastLayout {
		s.broadcastLayoutNow()
	}
}

type clipboardEvent struct {
	paneID uint32
	data   []byte
}

func (e clipboardEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.ensureWaiters().recordClipboard(e.data)
	s.broadcastNow(&Message{Type: MsgTypeClipboard, PaneID: e.paneID, PaneData: e.data})
}

type crashCheckpointWrittenEvent struct {
	path string
}

func (e crashCheckpointWrittenEvent) handle(s *Session) {
	if e.path == "" {
		return
	}
	s.ensureWaiters().recordCrashCheckpoint(e.path)
}

type idleTimeoutEvent struct {
	paneID uint32
}

func (e idleTimeoutEvent) handle(s *Session) {
	s.idle.MarkIdle(e.paneID)

	// Refresh CWD/branch off the event loop to avoid blocking on lsof/git.
	// Tests can disable this background path to keep integration timing
	// deterministic when they need stable snapshots.
	if p := s.findPaneByID(e.paneID); p != nil && !p.IsProxy() && !s.DisablePaneMetaAutoRefresh {
		pane := p
		go func() {
			cwd, branch := s.detectPaneCwdBranch(pane)
			s.enqueueEvent(cwdBranchResultEvent{paneID: e.paneID, cwd: cwd, branch: branch})
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
	if pane != nil && pane.AgentStatus().Idle {
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

func (e vtIdleTimeoutEvent) handle(s *Session) {
	if s.vtIdle == nil {
		return
	}
	if !s.vtIdle.MarkSettled(e.paneID, e.lastOutput) {
		return
	}
}

type cwdBranchResultEvent struct {
	paneID uint32
	cwd    string
	branch string
}

func (e cwdBranchResultEvent) handle(s *Session) {
	if p := s.findPaneByID(e.paneID); p != nil {
		p.ApplyCwdBranch(e.cwd, e.branch)
		s.broadcastLayoutNow()
	}
}

type metaUpdateEvent struct {
	paneID uint32
	update mux.MetaUpdate
}

func (e metaUpdateEvent) handle(s *Session) {
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
		if *e.update.Branch == "" {
			_ = removePaneKVValue(p, mux.PaneMetaKeyBranch)
		} else {
			_ = setPaneKVValue(p, mux.PaneMetaKeyBranch, *e.update.Branch)
		}
	}
	s.broadcastLayoutNow()
}

type takeoverEvent struct {
	srv    *Server
	paneID uint32
	req    mux.TakeoverRequest
}

func (e takeoverEvent) handle(s *Session) {
	go s.handleTakeover(e.paneID, e.req)
}

type remotePaneExitEvent struct {
	paneID uint32
	reason string
}

func (e remotePaneExitEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.handleFinalizedPaneRemoval(e.paneID, false, e.reason)
}

type remoteStateChangeEvent struct {
	hostName string
	state    proto.ConnState
}

func (e remoteStateChangeEvent) handle(s *Session) {
	for _, p := range s.Panes {
		if p.Meta.Host == e.hostName && p.IsProxy() {
			p.Meta.Remote = string(e.state)
		}
	}
	s.broadcastLayoutNow()
}

type localPaneBuildResultEvent struct {
	placeholder *mux.Pane
	pane        *mux.Pane
	err         error
	done        chan error
}

func (e localPaneBuildResultEvent) handle(s *Session) {
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
	s.enqueueEvent(paneOutputEvent{paneID: paneID, data: data, seq: seq})
}

func (s *Session) enqueuePaneExit(paneID uint32, reason string) {
	s.enqueueEvent(paneExitEvent{paneID: paneID, reason: reason})
}

func (s *Session) enqueuePaneCleanupTimeout(paneID uint32) {
	s.enqueueEvent(paneCleanupTimeoutEvent{paneID: paneID})
}

func (s *Session) enqueueClipboard(paneID uint32, data []byte) {
	s.enqueueEvent(clipboardEvent{paneID: paneID, data: data})
}

func (s *Session) enqueueIdleTimeout(paneID uint32) {
	s.enqueueEvent(idleTimeoutEvent{paneID: paneID})
}

func (s *Session) enqueueVTIdleTimeout(paneID uint32, lastOutput time.Time) {
	s.enqueueEvent(vtIdleTimeoutEvent{paneID: paneID, lastOutput: lastOutput})
}

func (s *Session) enqueueTakeover(srv *Server, paneID uint32, req mux.TakeoverRequest) {
	s.enqueueEvent(takeoverEvent{srv: srv, paneID: paneID, req: req})
}

func (s *Session) enqueueRemotePaneExit(paneID uint32, reason string) {
	s.enqueueEvent(remotePaneExitEvent{paneID: paneID, reason: reason})
}

func (s *Session) enqueueRemoteStateChange(hostName string, state proto.ConnState) {
	s.enqueueEvent(remoteStateChangeEvent{hostName: hostName, state: state})
}

// --- Pane output subscribe/unsubscribe through the event loop ---

type paneOutputSubscribeCmd struct {
	paneID uint32
	reply  chan chan struct{}
}

func (e paneOutputSubscribeCmd) handle(s *Session) {
	ch := s.addPaneOutputSubscriber(e.paneID)
	e.reply <- ch
}

type paneOutputUnsubscribeCmd struct {
	paneID uint32
	ch     chan struct{}
}

func (e paneOutputUnsubscribeCmd) handle(s *Session) {
	s.ensureWaiters().removePaneOutputSubscriber(e.paneID, e.ch)
}

func (s *Session) enqueuePaneOutputSubscribe(paneID uint32) chan struct{} {
	reply := make(chan (chan struct{}), 1)
	if !s.enqueueEvent(paneOutputSubscribeCmd{paneID: paneID, reply: reply}) {
		return nil
	}
	select {
	case ch := <-reply:
		return ch
	case <-s.sessionEventDone:
		return nil
	}
}

func (s *Session) enqueuePaneOutputUnsubscribe(paneID uint32, ch chan struct{}) {
	s.enqueueEvent(paneOutputUnsubscribeCmd{paneID: paneID, ch: ch})
}
