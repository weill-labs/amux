package server

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

var errSessionShuttingDown = errors.New("session shutting down")

type sessionEvent = eventloop.Command[Session]

type sessionAction interface {
	handle(*Session)
}

type sessionEventCommand struct {
	sessionAction
}

func (c sessionEventCommand) Handle(s *Session) {
	c.sessionAction.handle(s)
}

type commandMutationResult struct {
	output          string
	err             error
	bell            bool
	broadcastLayout bool
	paneHistories   []paneHistoryUpdate
	paneRenders     []paneRender
	startPanes      []*mux.Pane
	closePanes      []*mux.Pane
	sendExit        bool
	shutdownServer  bool // handled by caller goroutine, not event loop
}

type paneHistoryUpdate struct {
	paneID  uint32
	history []string
}

type paneRender struct {
	paneID uint32
	data   []byte
}

type attachPaneSnapshot struct {
	paneID    uint32
	history   []string
	screen    []byte
	outputSeq uint64
}

type attachResult struct {
	snap              *proto.LayoutSnapshot
	paneSnapshots     []attachPaneSnapshot
	newPane           *mux.Pane
	layoutBroadcasted bool
	err               error
}

type ensureInitialWindowResult struct {
	newPane       *mux.Pane
	layoutChanged bool
}

type attachClientEvent struct {
	srv   *Server
	cc    *clientConn
	cols  int
	rows  int
	reply chan attachResult
}

func (e attachClientEvent) handle(s *Session) {
	e.reply <- s.handleAttachEvent(e.srv, e.cc, e.cols, e.rows)
}

func (s *Session) recoverInitialWindowFromOrphansLocked(cols, rows int) (bool, error) {
	if len(s.Windows) > 0 {
		return false, nil
	}

	var orphans []*mux.Pane
	for _, pane := range s.Panes {
		if pane.Meta.Dormant || s.findWindowByPaneID(pane.ID) != nil {
			continue
		}
		orphans = append(orphans, pane)
	}
	if len(orphans) == 0 {
		return false, nil
	}

	layoutH := rows - render.GlobalBarHeight
	winID := s.windowCounter.Add(1)
	w := mux.NewWindow(orphans[0], cols, layoutH)
	w.ID = winID
	w.Name = fmt.Sprintf(WindowNameFormat, winID)
	for _, pane := range orphans[1:] {
		if _, err := w.Split(mux.SplitVertical, pane); err != nil {
			return false, err
		}
	}
	w.LeadPaneID = orphans[0].ID
	s.Windows = append(s.Windows, w)
	s.ActiveWindowID = winID
	return true, nil
}

// ensureInitialWindowLocked creates the first window and pane for an empty
// session using the provided terminal size. If orphaned panes already exist
// without any window, it rehabilitates them into a recovery window instead of
// allocating a fresh pane. Event-loop only.
func (s *Session) ensureInitialWindowLocked(srv *Server, cols, rows int, preferred *clientConn) (ensureInitialWindowResult, error) {
	if len(s.Windows) > 0 {
		return ensureInitialWindowResult{}, nil
	}
	if recovered, err := s.recoverInitialWindowFromOrphansLocked(cols, rows); err != nil {
		return ensureInitialWindowResult{}, err
	} else if recovered {
		return ensureInitialWindowResult{layoutChanged: true}, nil
	}

	layoutH := rows - render.GlobalBarHeight
	paneH := mux.PaneContentHeight(layoutH)
	pane, err := s.createPaneWithMetaForColorProfile(srv, mux.PaneMeta{}, cols, paneH, s.paneLaunchColorProfile(preferred))
	if err != nil {
		return ensureInitialWindowResult{}, err
	}

	winID := s.windowCounter.Add(1)
	w := mux.NewWindow(pane, cols, layoutH)
	w.ID = winID
	w.LeadPaneID = pane.ID
	w.Name = fmt.Sprintf(WindowNameFormat, winID)
	s.Windows = append(s.Windows, w)
	s.ActiveWindowID = winID
	return ensureInitialWindowResult{newPane: pane, layoutChanged: true}, nil
}

type commandMutationEvent struct {
	fn    func(*Session) commandMutationResult
	reply chan commandMutationResult
}

func (e commandMutationEvent) handle(s *Session) {
	res := recoverCommandMutation(e.fn, s)
	if res.err == nil {
		s.ensureInputRouter().syncPanes(s.Panes)
		// Keep enqueueCommandMutation callers from observing stale input routing
		// after focus/window mutations return.
		s.refreshInputTarget()
		if res.broadcastLayout {
			s.broadcastLayoutNow()
			res.broadcastLayout = false
		}
		for _, ph := range res.paneHistories {
			s.broadcastPaneHistoryNow(ph.paneID, ph.history)
		}
		res.paneHistories = nil
		for _, pr := range res.paneRenders {
			s.broadcastPaneOutputNow(pr.paneID, pr.data, 0)
		}
		res.paneRenders = nil
	}
	e.reply <- res
}

// recoverCommandMutation calls fn and converts any panic into an error result
// so the event loop keeps running and the reply channel is always written.
func recoverCommandMutation(fn func(*Session) commandMutationResult, s *Session) (res commandMutationResult) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[amux] panic in command mutation: %v\n%s", r, debug.Stack())
			res = commandMutationResult{err: fmt.Errorf("internal error: %v", r)}
		}
	}()
	return fn(s)
}

type detachClientEvent struct {
	cc     *clientConn
	reason string
}

func (e detachClientEvent) handle(s *Session) {
	if !s.hasClient(e.cc) {
		return
	}
	s.appendConnectionLog(connectionLogEventDetach, e.cc.ID, e.cc.cols, e.cc.rows, e.cc.disconnectReasonValue())
	s.emitClientDisconnectEvent(e.cc, e.reason)
	s.removeClient(e.cc)
}

type resizeClientEvent struct {
	cc   *clientConn
	cols int
	rows int
}

func (e resizeClientEvent) handle(s *Session) {
	e.cc.cols = e.cols
	e.cc.rows = e.rows
	s.noteClientActivity(e.cc)
	s.recalcSize()
	s.broadcastLayoutNow()
}

type clientActivityEvent struct {
	cc *clientConn
}

func (e clientActivityEvent) handle(s *Session) {
	if !s.noteClientActivity(e.cc) {
		return
	}
	s.recalcSize()
	s.broadcastLayoutNow()
}

type liveInputEvent struct {
	cc   *clientConn
	data []byte
}

func (e liveInputEvent) handle(s *Session) {
	pane := s.ensureInputRouter().activeInputPaneForWriteOnActor(s, e.cc)
	if pane == nil {
		return
	}
	if err := s.enqueueLivePaneInput(pane, e.data); err != nil && !errors.Is(err, errPacedInputClosed) {
		log.Printf("[amux] live input %s: %v", pane.Meta.Name, err)
	}
}

type liveInputPaneEvent struct {
	paneID uint32
	data   []byte
}

func (e liveInputPaneEvent) handle(s *Session) {
	pane := s.ensureInputRouter().paneByIDOnActor(s, e.paneID)
	if pane == nil {
		return
	}
	if err := s.enqueueLivePaneInput(pane, e.data); err != nil && !errors.Is(err, errPacedInputClosed) {
		log.Printf("[amux] live input %s: %v", pane.Meta.Name, err)
	}
}

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

func (s *Session) startEventLoop() {
	s.sessionEvents = make(chan sessionEvent, 128)
	s.sessionEventStop = make(chan struct{})
	s.sessionEventDone = make(chan struct{})
	go func() {
		s.eventLoopOwner.Assert("server.Session", "eventLoop")
		eventloop.Run(s, s.sessionEvents, s.sessionEventStop, s.sessionEventDone, func(s *Session) bool {
			// Keep the active input target in sync with actor-owned focus/window
			// state so the common input path can avoid a round-trip through the
			// session queue.
			s.refreshInputTarget()
			if !s.wantShutdown {
				return true
			}
			// Trigger shutdown asynchronously — Shutdown() waits on
			// sessionEventDone, so the loop must exit first.
			go s.exitServer.Shutdown()
			return false
		})
	}()
}

func (s *Session) stopEventLoop() {
	if s.sessionEventStop == nil || s.sessionEventDone == nil {
		return
	}
	select {
	case <-s.sessionEventDone:
		return
	default:
	}
	close(s.sessionEventStop)
	<-s.sessionEventDone
}

func (s *Session) enqueueEvent(ev sessionAction) bool {
	return eventloop.Enqueue(s.sessionEvents, s.sessionEventStop, sessionEventCommand{sessionAction: ev})
}

func (s *Session) enqueueAttachClient(srv *Server, cc *clientConn, cols, rows int) attachResult {
	reply := make(chan attachResult, 1)
	if !s.enqueueEvent(attachClientEvent{
		srv:   srv,
		cc:    cc,
		cols:  cols,
		rows:  rows,
		reply: reply,
	}) {
		return attachResult{err: errSessionShuttingDown}
	}
	select {
	case res := <-reply:
		return res
	case <-s.sessionEventDone:
		return attachResult{err: errSessionShuttingDown}
	}
}

func (s *Session) enqueueCommandMutation(fn func(*Session) commandMutationResult) commandMutationResult {
	reply := make(chan commandMutationResult, 1)
	if !s.enqueueEvent(commandMutationEvent{
		fn:    fn,
		reply: reply,
	}) {
		return commandMutationResult{err: errSessionShuttingDown}
	}
	select {
	case res := <-reply:
		return res
	case <-s.sessionEventDone:
		// The event loop exited (e.g., wantShutdown after our handler).
		// The reply may already be buffered — prefer it over the error.
		select {
		case res := <-reply:
			return res
		default:
			return commandMutationResult{err: errSessionShuttingDown}
		}
	}
}

func (s *Session) enqueueDetachClient(cc *clientConn, reason string) {
	s.enqueueEvent(detachClientEvent{cc: cc, reason: reason})
}

func (s *Session) enqueueResizeClient(cc *clientConn, cols, rows int) {
	s.enqueueEvent(resizeClientEvent{cc: cc, cols: cols, rows: rows})
}

func (s *Session) enqueueLiveInput(cc *clientConn, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	return s.enqueueEvent(liveInputEvent{cc: cc, data: append([]byte(nil), data...)})
}

func (s *Session) enqueueLiveInputPane(paneID uint32, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	return s.enqueueEvent(liveInputPaneEvent{paneID: paneID, data: append([]byte(nil), data...)})
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

// --- Event subscribe/unsubscribe through the event loop ---

// eventSubscribeResult is returned by enqueueEventSubscribe.
type eventSubscribeResult struct {
	sub          *eventSub
	initialState [][]byte // JSON-encoded initial events (only when sendInitial is true)
}

type uiWaitSubscription struct {
	sub          *eventSub
	clientID     string
	currentMatch bool
	currentGen   uint64
}

type uiWaitSubscribeResult struct {
	subscription uiWaitSubscription
	err          error
}

type eventSubscribeCmd struct {
	filter      eventFilter
	sendInitial bool // if true, compute and return current-state events atomically
	reply       chan eventSubscribeResult
}

func (e eventSubscribeCmd) handle(s *Session) {
	sub := eventloop.Subscribe(&s.eventSubs, e.filter)

	result := eventSubscribeResult{sub: sub}
	if e.sendInitial {
		result.initialState = eventloop.MarshalMatching(s.currentStateEvents(), e.filter)
	}
	e.reply <- result
}

type uiWaitSubscribeCmd struct {
	requestedClientID string
	eventName         string
	reply             chan uiWaitSubscribeResult
}

func (e uiWaitSubscribeCmd) handle(s *Session) {
	client, err := s.resolveUIClientSnapshot(e.requestedClientID, e.eventName)
	if err != nil {
		e.reply <- uiWaitSubscribeResult{err: err}
		return
	}

	sub := eventloop.Subscribe(&s.eventSubs, eventFilter{
		Types:    []string{e.eventName},
		ClientID: client.clientID,
	})

	e.reply <- uiWaitSubscribeResult{subscription: uiWaitSubscription{
		sub:          sub,
		clientID:     client.clientID,
		currentMatch: client.currentMatch,
		currentGen:   client.currentGen,
	}}
}

type eventUnsubscribeCmd struct {
	sub *eventSub
}

func (e eventUnsubscribeCmd) handle(s *Session) {
	eventloop.Unsubscribe(&s.eventSubs, e.sub)
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

// --- UI events through the event loop ---

type uiEventCmd struct {
	cc      *clientConn
	uiEvent string
}

func (e uiEventCmd) handle(s *Session) {
	activityChanged := s.noteClientActivity(e.cc)
	if e.uiEvent == proto.UIEventClientFocusGained {
		if activityChanged {
			s.recalcSize()
			s.broadcastLayoutNow()
		}
		e.cc.uiGeneration++
		s.emitEvent(Event{Type: e.uiEvent, ClientID: e.cc.ID})
		return
	}
	changed, err := e.cc.applyUIEvent(e.uiEvent)
	clientID := e.cc.ID
	if err != nil {
		e.cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return
	}
	if activityChanged {
		s.recalcSize()
		s.broadcastLayoutNow()
	}
	if changed {
		e.cc.uiGeneration++
		s.emitEvent(Event{Type: e.uiEvent, ClientID: clientID})
	}
}

// --- emitEvent replaces EventBus.Emit ---

// emitEvent marshals an event and sends it to all matching subscribers.
// Non-blocking: if a subscriber's channel is full the event is dropped.
// Must be called from the session event loop (no mutex needed).
func (s *Session) emitEvent(ev Event) {
	eventloop.Emit(s.eventSubs, ev)
}

// --- Enqueue helpers ---

func (s *Session) enqueueEventSubscribe(f eventFilter, sendInitial bool) eventSubscribeResult {
	reply := make(chan eventSubscribeResult, 1)
	if !s.enqueueEvent(eventSubscribeCmd{filter: f, sendInitial: sendInitial, reply: reply}) {
		return eventSubscribeResult{}
	}
	select {
	case res := <-reply:
		return res
	case <-s.sessionEventDone:
		return eventSubscribeResult{}
	}
}

// enqueueUIWaitSubscribe resolves the target client, installs the event
// subscription, and snapshots whether the client already matches the requested
// UI state in one event-loop turn. That closes the race where a UI transition
// could land between a separate query and subscribe call.
func (s *Session) enqueueUIWaitSubscribe(requestedClientID, eventName string) (uiWaitSubscription, error) {
	var zero uiWaitSubscription

	reply := make(chan uiWaitSubscribeResult, 1)
	if !s.enqueueEvent(uiWaitSubscribeCmd{
		requestedClientID: requestedClientID,
		eventName:         eventName,
		reply:             reply,
	}) {
		return zero, errSessionShuttingDown
	}

	select {
	case res := <-reply:
		if res.err != nil {
			return zero, res.err
		}
		return res.subscription, nil
	case <-s.sessionEventDone:
		select {
		case res := <-reply:
			if res.err != nil {
				return zero, res.err
			}
			return res.subscription, nil
		default:
			return zero, errSessionShuttingDown
		}
	}
}

func (s *Session) enqueueEventUnsubscribe(sub *eventSub) {
	s.enqueueEvent(eventUnsubscribeCmd{sub: sub})
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

func (s *Session) enqueueUIEvent(cc *clientConn, uiEvent string) {
	s.enqueueEvent(uiEventCmd{cc: cc, uiEvent: uiEvent})
}

func (s *Session) enqueueClientActivity(cc *clientConn) {
	s.enqueueEvent(clientActivityEvent{cc: cc})
}

func (s *Session) emitClientConnectEvent(cc *clientConn) {
	if cc == nil {
		return
	}
	s.emitEvent(Event{Type: EventClientConnect, ClientID: cc.ID})
}

func (s *Session) emitClientDisconnectEvent(cc *clientConn, reason string) {
	if cc == nil {
		return
	}
	s.emitEvent(Event{
		Type:     EventClientDisconnect,
		ClientID: cc.ID,
		Reason:   reason,
	})
}

// disconnectClientsForReload preserves the current layout snapshot by removing
// clients without recalculating the live session size or broadcasting layout.
func (s *Session) disconnectClientsForReload(clients []*clientConn) {
	for _, cc := range clients {
		if !s.hasClient(cc) {
			continue
		}
		s.appendConnectionLog(connectionLogEventDetach, cc.ID, cc.cols, cc.rows, DisconnectReasonServerReload)
		s.emitClientDisconnectEvent(cc, DisconnectReasonServerReload)
		s.removeClientWithoutLayout(cc)
	}
}

func (s *Session) handleAttachEvent(srv *Server, cc *clientConn, cols, rows int) attachResult {
	idleSnap := s.snapshotIdleState()
	countsForExitUnattached := cc.participatesInSizeNegotiation()

	cc.cols = cols
	cc.rows = rows

	res := attachResult{}
	w := s.activeWindow()
	oldWidth, oldHeight := 0, 0
	if w != nil {
		oldWidth = w.Width
		oldHeight = w.Height
	}

	initRes, err := s.ensureInitialWindowLocked(srv, cols, rows, cc)
	if err != nil {
		res.err = err
		return res
	}
	res.newPane = initRes.newPane

	s.ensureClientManager().addClient(cc)
	if countsForExitUnattached {
		s.hadClient = true
	}
	s.appendConnectionLog(connectionLogEventAttach, cc.ID, cc.cols, cc.rows, "")
	s.noteClientActivity(cc)
	s.emitClientConnectEvent(cc)
	s.recalcSize()
	if aw := s.activeWindow(); aw != nil {
		res.layoutBroadcasted = aw.Width != oldWidth || aw.Height != oldHeight
	}
	if initRes.layoutChanged || res.layoutBroadcasted {
		s.broadcastLayoutNow()
		res.layoutBroadcasted = true
	}

	res.snap = s.snapshotLayout(idleSnap)
	for _, p := range s.Panes {
		history, screen, seq := p.HistoryScreenSnapshot()
		res.paneSnapshots = append(res.paneSnapshots, attachPaneSnapshot{
			paneID:    p.ID,
			history:   history,
			screen:    []byte(screen),
			outputSeq: seq,
		})
	}

	return res
}
