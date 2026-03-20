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

type attachResult struct {
	snap        *proto.LayoutSnapshot
	paneRenders []paneRender
	newPane     *mux.Pane
	err         error
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

// ensureInitialWindowLocked creates the first window and pane for an empty
// session using the provided terminal size. Caller must hold s.mu.
func (s *Session) ensureInitialWindowLocked(srv *Server, cols, rows int) (*mux.Pane, error) {
	if len(s.Windows) > 0 {
		return nil, nil
	}

	layoutH := rows - render.GlobalBarHeight
	paneH := mux.PaneContentHeight(layoutH)
	pane, err := s.createPane(srv, cols, paneH)
	if err != nil {
		return nil, err
	}

	winID := s.windowCounter.Add(1)
	w := mux.NewWindow(pane, cols, layoutH)
	w.ID = winID
	w.Name = fmt.Sprintf(WindowNameFormat, winID)
	s.Windows = append(s.Windows, w)
	s.ActiveWindowID = winID
	return pane, nil
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
	s.mu.Lock()
	e.cc.cols = e.cols
	e.cc.rows = e.rows
	s.recalcSizeLocked()
	s.mu.Unlock()
	s.broadcastLayout()
}

type paneOutputEvent struct {
	paneID uint32
	data   []byte
}

func (e paneOutputEvent) handle(s *Session) {
	s.broadcastPaneOutput(e.paneID, e.data)
}

type paneExitEvent struct {
	paneID uint32
}

func (e paneExitEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.mu.Lock()
	if !s.hasPane(e.paneID) {
		s.mu.Unlock()
		return
	}
	if len(s.Panes) <= 1 {
		s.mu.Unlock()
		s.broadcast(&Message{Type: MsgTypeExit})
		s.wantShutdown = true
		return
	}
	s.removePane(e.paneID)
	s.closePaneInWindow(e.paneID)
	s.mu.Unlock()
	s.broadcastLayout()
}

type clipboardEvent struct {
	paneID uint32
	data   []byte
}

func (e clipboardEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.broadcast(&Message{Type: MsgTypeClipboard, PaneID: e.paneID, PaneData: e.data})

	s.clipboardMu.Lock()
	s.lastClipboardB64 = string(e.data)
	s.clipboardGen.Add(1)
	s.clipboardCond.Broadcast()
	s.clipboardMu.Unlock()
}

type idleTimeoutEvent struct {
	paneID uint32
}

func (e idleTimeoutEvent) handle(s *Session) {
	s.idle.MarkIdle(e.paneID)
	env := s.buildPaneEnv(e.paneID, hooks.OnIdle)
	s.fireHooks(hooks.OnIdle, env)
	s.emitEvent(Event{
		Type:     EventIdle,
		PaneID:   e.paneID,
		PaneName: env["AMUX_PANE_NAME"],
		Host:     env["AMUX_HOST"],
	})
	s.broadcastLayout()
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
	s.mu.Lock()
	if !s.hasPane(e.paneID) {
		s.mu.Unlock()
		return
	}
	s.removePane(e.paneID)
	s.closePaneInWindow(e.paneID)
	s.mu.Unlock()
	s.broadcastLayout()
}

type remoteStateChangeEvent struct {
	hostName string
	state    remote.ConnState
}

func (e remoteStateChangeEvent) handle(s *Session) {
	s.mu.Lock()
	for _, p := range s.Panes {
		if p.Meta.Host == e.hostName && p.IsProxy() {
			p.Meta.Remote = string(e.state)
		}
	}
	s.mu.Unlock()
	s.broadcastLayout()
}

type hookResultEvent struct {
	record hookResultRecord
}

func (e hookResultEvent) handle(s *Session) {
	s.hookMu.Lock()
	e.record.Generation = s.hookGen.Add(1)
	s.hookResults = append(s.hookResults, e.record)
	if len(s.hookResults) > 128 {
		s.hookResults = append([]hookResultRecord(nil), s.hookResults[len(s.hookResults)-128:]...)
	}
	s.hookCond.Broadcast()
	s.hookMu.Unlock()

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

func (s *Session) enqueuePaneOutput(paneID uint32, data []byte) {
	s.enqueueEvent(paneOutputEvent{paneID: paneID, data: data})
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
	s.mu.Lock()
	changed, err := e.cc.applyUIEvent(e.uiEvent)
	clientID := e.cc.ID
	s.mu.Unlock()
	if err != nil {
		e.cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()})
		return
	}
	if changed {
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

// emitCmd routes an emitEvent call through the event loop.
// Used by broadcastLayout which can be called from any goroutine.
type emitCmd struct {
	ev Event
}

func (e emitCmd) handle(s *Session) {
	s.emitEvent(e.ev)
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

func (s *Session) handleAttachEvent(srv *Server, cc *ClientConn, cols, rows int) attachResult {
	idleSnap := s.snapshotIdleState()
	s.mu.Lock()
	defer s.mu.Unlock()

	cc.cols = cols
	cc.rows = rows

	res := attachResult{}

	pane, err := s.ensureInitialWindowLocked(srv, cols, rows)
	if err != nil {
		res.err = err
		return res
	}
	res.newPane = pane

	s.clients = append(s.clients, cc)
	s.hadClient = true
	s.recalcSizeLocked()

	res.snap = s.snapshotLayoutLocked(idleSnap)
	for _, p := range s.Panes {
		res.paneRenders = append(res.paneRenders, paneRender{
			paneID: p.ID,
			data:   []byte(p.RenderScreen()),
		})
	}

	return res
}
