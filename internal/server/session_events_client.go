package server

import (
	"context"
	"errors"

	"github.com/weill-labs/amux/internal/eventloop"
	"github.com/weill-labs/amux/internal/proto"
)

type attachPaneSnapshot struct {
	paneID         uint32
	styledHistory  []proto.StyledLine
	historyVersion uint64
	historyCache   *proto.PaneHistoryPayloadCache
	screen         []byte
	outputSeq      uint64
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

func (e attachClientEvent) handle(_ context.Context, s *Session) {
	e.reply <- s.handleAttachEvent(e.srv, e.cc, e.cols, e.rows)
}

type detachClientEvent struct {
	cc     *clientConn
	reason string
}

func (e detachClientEvent) handle(_ context.Context, s *Session) {
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

func (e resizeClientEvent) handle(_ context.Context, s *Session) {
	e.cc.cols = e.cols
	e.cc.rows = e.rows
	s.noteClientActivity(e.cc)
	s.recalcSize()
	s.broadcastLayoutNow()
}

type liveInputEvent struct {
	cc    *clientConn
	data  []byte
	epoch uint32
}

func (e liveInputEvent) handle(_ context.Context, s *Session) {
	pane := s.ensureInputRouter().activeInputPaneForWriteOnActor(s, e.cc)
	if pane == nil {
		return
	}
	if e.cc != nil {
		e.cc.notePredictionEpoch(pane.ID, e.epoch, e.data)
	}
	s.logLiveInputError(pane.ID, pane.Meta.Name, s.enqueueLivePaneInput(pane, e.data))
}

type liveInputPaneEvent struct {
	cc     *clientConn
	paneID uint32
	data   []byte
	epoch  uint32
}

func (e liveInputPaneEvent) handle(_ context.Context, s *Session) {
	pane := s.ensureInputRouter().paneByIDOnActor(s, e.paneID)
	if pane == nil {
		return
	}
	if e.cc != nil {
		e.cc.notePredictionEpoch(pane.ID, e.epoch, e.data)
	}
	s.logLiveInputError(pane.ID, pane.Meta.Name, s.enqueueLivePaneInput(pane, e.data))
}

func (s *Session) logLiveInputError(paneID uint32, paneName string, err error) {
	if err == nil || errors.Is(err, errPacedInputClosed) {
		return
	}
	fields := []any{
		"event", "live_input",
		"pane_id", paneID,
		"pane_name", paneName,
		"error", err,
	}
	if errors.Is(err, errPacedInputBackpressure) {
		s.logger.Debug("live input backpressure", fields...)
		return
	}
	s.logger.Warn("live input failed", fields...)
}

func (s *Session) enqueueAttachClient(srv *Server, cc *clientConn, cols, rows int) attachResult {
	ctx := s.context()
	if cc != nil {
		ctx = cc.context()
	}
	reply := make(chan attachResult, 1)
	if !s.enqueueEvent(ctx, attachClientEvent{
		srv:   srv,
		cc:    cc,
		cols:  cols,
		rows:  rows,
		reply: reply,
	}) {
		if err := ctx.Err(); err != nil {
			return attachResult{err: err}
		}
		return attachResult{err: errSessionShuttingDown}
	}
	select {
	case res := <-reply:
		return res
	case <-ctx.Done():
		return attachResult{err: ctx.Err()}
	case <-s.sessionEventDone:
		return attachResult{err: errSessionShuttingDown}
	}
}

func (s *Session) enqueueDetachClient(cc *clientConn, reason string) {
	s.enqueueEvent(s.context(), detachClientEvent{cc: cc, reason: reason})
}

func (s *Session) enqueueResizeClient(cc *clientConn, cols, rows int) {
	ctx := s.context()
	if cc != nil {
		ctx = cc.context()
	}
	s.enqueueEvent(ctx, resizeClientEvent{cc: cc, cols: cols, rows: rows})
}

func (s *Session) enqueueLiveInputWithEpoch(cc *clientConn, data []byte, epoch uint32) bool {
	if len(data) == 0 {
		return true
	}
	ctx := s.context()
	if cc != nil {
		ctx = cc.context()
	}
	return s.enqueueEvent(ctx, liveInputEvent{cc: cc, data: append([]byte(nil), data...), epoch: epoch})
}

func (s *Session) enqueueLiveInputPane(paneID uint32, data []byte) bool {
	return s.enqueueLiveInputPaneFromClient(nil, paneID, data, 0)
}

func (s *Session) enqueueLiveInputPaneFromClient(cc *clientConn, paneID uint32, data []byte, epoch uint32) bool {
	if len(data) == 0 {
		return true
	}
	ctx := s.context()
	if cc != nil {
		ctx = cc.context()
	}
	return s.enqueueEvent(ctx, liveInputPaneEvent{cc: cc, paneID: paneID, data: append([]byte(nil), data...), epoch: epoch})
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

func (e eventSubscribeCmd) handle(ctx context.Context, s *Session) {
	sub := eventloop.Subscribe(&s.eventSubs, e.filter)

	result := eventSubscribeResult{sub: sub}
	if e.sendInitial {
		result.initialState = eventloop.MarshalMatching(s.currentStateEvents(), e.filter)
	}
	select {
	case e.reply <- result:
	case <-ctx.Done():
		eventloop.Unsubscribe(&s.eventSubs, sub)
	}
}

type uiWaitSubscribeCmd struct {
	requestedClientID string
	eventName         string
	reply             chan uiWaitSubscribeResult
}

func (e uiWaitSubscribeCmd) handle(ctx context.Context, s *Session) {
	client, err := s.resolveUIClientSnapshot(e.requestedClientID, e.eventName)
	if err != nil {
		select {
		case e.reply <- uiWaitSubscribeResult{err: err}:
		case <-ctx.Done():
		}
		return
	}

	sub := eventloop.Subscribe(&s.eventSubs, eventFilter{
		Types:    []string{e.eventName},
		ClientID: client.clientID,
	})

	result := uiWaitSubscribeResult{subscription: uiWaitSubscription{
		sub:          sub,
		clientID:     client.clientID,
		currentMatch: client.currentMatch,
		currentGen:   client.currentGen,
	}}
	select {
	case e.reply <- result:
	case <-ctx.Done():
		eventloop.Unsubscribe(&s.eventSubs, sub)
	}
}

type eventUnsubscribeCmd struct {
	sub *eventSub
}

func (e eventUnsubscribeCmd) handle(_ context.Context, s *Session) {
	eventloop.Unsubscribe(&s.eventSubs, e.sub)
}

// --- UI events through the event loop ---

type uiEventCmd struct {
	cc      *clientConn
	uiEvent string
}

func (e uiEventCmd) handle(_ context.Context, s *Session) {
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
		if sendErr := e.cc.Send(&Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}); sendErr != nil && e.cc.logger != nil {
			e.cc.logger.Warn("sending UI event error failed", "event", "ui_event", "ui_event", e.uiEvent, "error", sendErr)
		}
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

func (s *Session) enqueueEventSubscribe(ctx context.Context, f eventFilter, sendInitial bool) eventSubscribeResult {
	reply := make(chan eventSubscribeResult)
	if !s.enqueueEvent(ctx, eventSubscribeCmd{filter: f, sendInitial: sendInitial, reply: reply}) {
		return eventSubscribeResult{}
	}
	select {
	case res := <-reply:
		return res
	case <-ctx.Done():
		return eventSubscribeResult{}
	case <-s.sessionEventDone:
		return eventSubscribeResult{}
	}
}

// enqueueUIWaitSubscribe resolves the target client, installs the event
// subscription, and snapshots whether the client already matches the requested
// UI state in one event-loop turn. That closes the race where a UI transition
// could land between a separate query and subscribe call.
func (s *Session) enqueueUIWaitSubscribe(ctx context.Context, requestedClientID, eventName string) (uiWaitSubscription, error) {
	var zero uiWaitSubscription

	reply := make(chan uiWaitSubscribeResult)
	if !s.enqueueEvent(ctx, uiWaitSubscribeCmd{
		requestedClientID: requestedClientID,
		eventName:         eventName,
		reply:             reply,
	}) {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return zero, errSessionShuttingDown
	}

	select {
	case res := <-reply:
		if res.err != nil {
			return zero, res.err
		}
		return res.subscription, nil
	case <-ctx.Done():
		return zero, ctx.Err()
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
	s.enqueueEvent(s.context(), eventUnsubscribeCmd{sub: sub})
}

func (s *Session) enqueueUIEvent(cc *clientConn, uiEvent string) {
	ctx := s.context()
	if cc != nil {
		ctx = cc.context()
	}
	s.enqueueEvent(ctx, uiEventCmd{cc: cc, uiEvent: uiEvent})
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
	activeWindow := s.activeWindow()
	activeWindowPanes := windowPanes(activeWindow)
	activePaneIDs := make(map[uint32]struct{}, len(activeWindowPanes))
	for _, pane := range activeWindowPanes {
		activePaneIDs[pane.ID] = struct{}{}
	}
	for _, p := range s.Panes {
		snapshot := attachPaneSnapshot{paneID: p.ID}
		if _, ok := activePaneIDs[p.ID]; ok {
			history, screen, seq, historyVersion, historyCache := p.StyledHistoryScreenSnapshotWithCache()
			snapshot.styledHistory = history
			snapshot.historyVersion = historyVersion
			snapshot.historyCache = historyCache
			snapshot.screen = []byte(screen)
			snapshot.outputSeq = seq
		} else {
			screen, seq := p.ScreenSnapshot()
			snapshot.screen = []byte(screen)
			snapshot.outputSeq = seq
		}
		res.paneSnapshots = append(res.paneSnapshots, snapshot)
	}

	return res
}
