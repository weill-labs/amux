package server

import (
	"errors"

	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/proto"
)

type attachPaneSnapshot struct {
	paneID        uint32
	styledHistory []proto.StyledLine
	screen        []byte
	outputSeq     uint64
}

type attachResult struct {
	snap              *proto.LayoutSnapshot
	paneSnapshots     []attachPaneSnapshot
	layoutBroadcasted bool
	err               error
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
	s.logClientDisconnect(e.cc, e.cc.disconnectReasonValue())
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
		s.logger.Warn("live input failed",
			"event", "live_input",
			"pane_id", pane.ID,
			"pane_name", pane.Meta.Name,
			"error", err,
		)
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
		s.logger.Warn("live input failed",
			"event", "live_input",
			"pane_id", pane.ID,
			"pane_name", pane.Meta.Name,
			"error", err,
		)
	}
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
		s.logClientDisconnect(cc, DisconnectReasonServerReload)
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

	s.ensureClientManager().addClient(cc)
	if countsForExitUnattached {
		s.hadClient = true
	}
	s.appendConnectionLog(connectionLogEventAttach, cc.ID, cc.cols, cc.rows, "")
	s.noteClientActivity(cc)
	s.emitClientConnectEvent(cc)
	s.logClientConnect(cc)
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
		history, screen, seq := p.StyledHistoryScreenSnapshot()
		res.paneSnapshots = append(res.paneSnapshots, attachPaneSnapshot{
			paneID:        p.ID,
			styledHistory: history,
			screen:        []byte(screen),
			outputSeq:     seq,
		})
	}

	return res
}
