package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
	"github.com/weill-labs/amux/internal/render"
)

var errSessionShuttingDown = errors.New("session shutting down")

type sessionEvent interface {
	handle(*Session)
}

type commandMutationResult struct {
	output          string
	err             error
	broadcastLayout bool
	paneRenders     []paneRender
	startPanes      []*mux.Pane
	closePanes      []*mux.Pane
	sendExit        bool
	shutdownServer  bool // handled by caller goroutine, not event loop
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
	snap          *proto.LayoutSnapshot
	paneSnapshots []attachPaneSnapshot
	newPane       *mux.Pane
	err           error
}

type ensureInitialWindowResult struct {
	newPane       *mux.Pane
	layoutChanged bool
}

type attachClientEvent struct {
	srv   *Server
	cc    *ClientConn
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
		if pane.Meta.Dormant || s.FindWindowByPaneID(pane.ID) != nil {
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
	s.Windows = append(s.Windows, w)
	s.ActiveWindowID = winID
	return true, nil
}

// ensureInitialWindowLocked creates the first window and pane for an empty
// session using the provided terminal size. If orphaned panes already exist
// without any window, it rehabilitates them into a recovery window instead of
// allocating a fresh pane. Event-loop only.
func (s *Session) ensureInitialWindowLocked(srv *Server, cols, rows int) (ensureInitialWindowResult, error) {
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
	pane, err := s.createPane(srv, cols, paneH)
	if err != nil {
		return ensureInitialWindowResult{}, err
	}

	winID := s.windowCounter.Add(1)
	w := mux.NewWindow(pane, cols, layoutH)
	w.ID = winID
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
	e.reply <- e.fn(s)
}

type detachClientEvent struct {
	cc *ClientConn
}

func (e detachClientEvent) handle(s *Session) {
	s.removeClient(e.cc)
}

type resizeClientEvent struct {
	cc   *ClientConn
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
	cc *ClientConn
}

func (e clientActivityEvent) handle(s *Session) {
	if !s.noteClientActivity(e.cc) {
		return
	}
	s.recalcSize()
	s.broadcastLayoutNow()
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
}

func (e paneExitEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	if !s.hasPane(e.paneID) {
		return
	}
	if len(s.Panes) <= 1 {
		s.broadcastNow(&Message{Type: MsgTypeExit})
		s.wantShutdown = true
		return
	}
	s.removePane(e.paneID)
	s.closePaneInWindow(e.paneID)
	s.broadcastLayoutNow()
}

type clipboardEvent struct {
	paneID uint32
	data   []byte
}

func (e clipboardEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.lastClipboardB64 = string(e.data)
	gen := s.clipboardGen.Add(1)
	s.notifyClipboardWaiters(gen, s.lastClipboardB64)
	s.broadcastNow(&Message{Type: MsgTypeClipboard, PaneID: e.paneID, PaneData: e.data})
}

type idleTimeoutEvent struct {
	paneID uint32
}

func (e idleTimeoutEvent) handle(s *Session) {
	s.idle.MarkIdle(e.paneID)

	// Refresh CWD/branch off the event loop to avoid blocking on lsof/git
	if p := s.findPaneByID(e.paneID); p != nil && !p.IsProxy() {
		go func() {
			cwd, branch := p.DetectCwdBranch()
			s.enqueueEvent(cwdBranchResultEvent{paneID: e.paneID, cwd: cwd, branch: branch})
		}()
	}

	env := s.buildPaneEnv(e.paneID, hooks.OnIdle)
	s.fireHooks(hooks.OnIdle, env)
	s.emitEvent(Event{
		Type:     EventIdle,
		PaneID:   e.paneID,
		PaneName: env["AMUX_PANE_NAME"],
		Host:     env["AMUX_HOST"],
	})
	s.broadcastLayoutNow()
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
		p.Meta.Task = *e.update.Task
	}
	if e.update.PR != nil {
		p.Meta.PR = *e.update.PR
	}
	if e.update.Branch != nil {
		if *e.update.Branch == "" {
			p.SetMetaManualBranch(false)
			p.Meta.GitBranch = ""
		} else {
			p.Meta.GitBranch = *e.update.Branch
			p.SetMetaManualBranch(true)
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
	go s.handleTakeover(e.srv, e.paneID, e.req)
}

type remotePaneExitEvent struct {
	paneID uint32
}

func (e remotePaneExitEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	if !s.hasPane(e.paneID) {
		return
	}
	s.removePane(e.paneID)
	s.closePaneInWindow(e.paneID)
	s.broadcastLayoutNow()
}

type remoteStateChangeEvent struct {
	hostName string
	state    remote.ConnState
}

func (e remoteStateChangeEvent) handle(s *Session) {
	for _, p := range s.Panes {
		if p.Meta.Host == e.hostName && p.IsProxy() {
			p.Meta.Remote = string(e.state)
		}
	}
	s.broadcastLayoutNow()
}

type hookResultEvent struct {
	record hookResultRecord
}

func (e hookResultEvent) handle(s *Session) {
	e.record.Generation = s.hookGen.Add(1)
	s.hookResults = append(s.hookResults, e.record)
	if len(s.hookResults) > 128 {
		s.hookResults = append([]hookResultRecord(nil), s.hookResults[len(s.hookResults)-128:]...)
	}
	s.notifyHookWaiters(e.record)

	s.emitEvent(Event{
		Type:       EventHook,
		Generation: e.record.Generation,
		PaneID:     e.record.PaneID,
		PaneName:   e.record.PaneName,
		Host:       e.record.Host,
		HookEvent:  e.record.Event,
		Command:    e.record.Command,
		Success:    e.record.Success,
		Error:      e.record.Error,
	})
}

func (s *Session) startEventLoop() {
	s.sessionEvents = make(chan sessionEvent, 128)
	s.sessionEventStop = make(chan struct{})
	s.sessionEventDone = make(chan struct{})
	go s.eventLoop()
}

func (s *Session) eventLoop() {
	defer close(s.sessionEventDone)
	for {
		select {
		case <-s.sessionEventStop:
			return
		case ev := <-s.sessionEvents:
			if ev != nil {
				ev.handle(s)
			}
			// Keep the active input target in sync with actor-owned focus/window
			// state so the common input path can avoid a round-trip through the
			// session queue.
			s.refreshInputTarget()
			if s.wantShutdown {
				// Trigger shutdown asynchronously — Shutdown() waits
				// on sessionEventDone, so we must return first.
				go s.exitServer.Shutdown()
				return
			}
		}
	}
}

func (s *Session) enqueueEvent(ev sessionEvent) bool {
	select {
	case <-s.sessionEventStop:
		return false
	default:
	}

	select {
	case <-s.sessionEventStop:
		return false
	case s.sessionEvents <- ev:
		return true
	}
}

func (s *Session) enqueueAttachClient(srv *Server, cc *ClientConn, cols, rows int) attachResult {
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

func (s *Session) enqueueDetachClient(cc *ClientConn) {
	s.enqueueEvent(detachClientEvent{cc: cc})
}

func (s *Session) enqueueResizeClient(cc *ClientConn, cols, rows int) {
	s.enqueueEvent(resizeClientEvent{cc: cc, cols: cols, rows: rows})
}

func (s *Session) enqueuePaneOutput(paneID uint32, data []byte, seq uint64) {
	s.enqueueEvent(paneOutputEvent{paneID: paneID, data: data, seq: seq})
}

func (s *Session) enqueuePaneExit(paneID uint32) {
	s.enqueueEvent(paneExitEvent{paneID: paneID})
}

func (s *Session) enqueueClipboard(paneID uint32, data []byte) {
	s.enqueueEvent(clipboardEvent{paneID: paneID, data: data})
}

func (s *Session) enqueueIdleTimeout(paneID uint32) {
	s.enqueueEvent(idleTimeoutEvent{paneID: paneID})
}

func (s *Session) enqueueTakeover(srv *Server, paneID uint32, req mux.TakeoverRequest) {
	s.enqueueEvent(takeoverEvent{srv: srv, paneID: paneID, req: req})
}

func (s *Session) enqueueRemotePaneExit(paneID uint32) {
	s.enqueueEvent(remotePaneExitEvent{paneID: paneID})
}

func (s *Session) enqueueRemoteStateChange(hostName string, state remote.ConnState) {
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
	sub := &eventSub{ch: make(chan []byte, 64), filter: e.filter}
	s.eventSubs = append(s.eventSubs, sub)

	result := eventSubscribeResult{sub: sub}
	if e.sendInitial {
		for _, ev := range s.currentStateEvents() {
			if e.filter.matches(ev) {
				data, _ := json.Marshal(ev)
				result.initialState = append(result.initialState, data)
			}
		}
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

	sub := &eventSub{
		ch:     make(chan []byte, 64),
		filter: eventFilter{Types: []string{e.eventName}, ClientID: client.clientID},
	}
	s.eventSubs = append(s.eventSubs, sub)

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
	for i, sub := range s.eventSubs {
		if sub == e.sub {
			s.eventSubs = append(s.eventSubs[:i], s.eventSubs[i+1:]...)
			break
		}
	}
}

// --- Pane output subscribe/unsubscribe through the event loop ---

type paneOutputSubscribeCmd struct {
	paneID uint32
	reply  chan chan struct{}
}

func (e paneOutputSubscribeCmd) handle(s *Session) {
	ch := make(chan struct{}, 1)
	if s.paneOutputSubs == nil {
		s.paneOutputSubs = make(map[uint32][]chan struct{})
	}
	s.paneOutputSubs[e.paneID] = append(s.paneOutputSubs[e.paneID], ch)
	e.reply <- ch
}

type paneOutputUnsubscribeCmd struct {
	paneID uint32
	ch     chan struct{}
}

func (e paneOutputUnsubscribeCmd) handle(s *Session) {
	subs := s.paneOutputSubs[e.paneID]
	for i, sub := range subs {
		if sub == e.ch {
			s.paneOutputSubs[e.paneID] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
}

// --- UI events through the event loop ---

type uiEventCmd struct {
	cc      *ClientConn
	uiEvent string
}

func (e uiEventCmd) handle(s *Session) {
	activityChanged := s.noteClientActivity(e.cc)
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
	ev.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	for _, sub := range s.eventSubs {
		if sub.filter.matches(ev) {
			select {
			case sub.ch <- data:
			default:
			}
		}
	}
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

func (s *Session) enqueueUIEvent(cc *ClientConn, uiEvent string) {
	s.enqueueEvent(uiEventCmd{cc: cc, uiEvent: uiEvent})
}

func (s *Session) enqueueClientActivity(cc *ClientConn) {
	s.enqueueEvent(clientActivityEvent{cc: cc})
}

func (s *Session) handleAttachEvent(srv *Server, cc *ClientConn, cols, rows int) attachResult {
	idleSnap := s.snapshotIdleState()

	cc.cols = cols
	cc.rows = rows

	res := attachResult{}

	initRes, err := s.ensureInitialWindowLocked(srv, cols, rows)
	if err != nil {
		res.err = err
		return res
	}
	res.newPane = initRes.newPane

	s.clients = append(s.clients, cc)
	s.hadClient = true
	s.noteClientActivity(cc)
	s.recalcSize()
	if initRes.layoutChanged {
		s.broadcastLayoutNow()
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
