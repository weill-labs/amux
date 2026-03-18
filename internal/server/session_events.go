package server

import (
	"errors"
	"fmt"

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
	srv    *Server
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
		go e.srv.Shutdown()
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
	s.Hooks.Fire(hooks.OnIdle, env)
	s.events.Emit(Event{
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

func (s *Session) enqueueDetachClient(cc *ClientConn) {
	s.enqueueEvent(detachClientEvent{cc: cc})
}

func (s *Session) enqueueResizeClient(cc *ClientConn, cols, rows int) {
	s.enqueueEvent(resizeClientEvent{cc: cc, cols: cols, rows: rows})
}

func (s *Session) enqueuePaneOutput(paneID uint32, data []byte) {
	s.enqueueEvent(paneOutputEvent{paneID: paneID, data: data})
}

func (s *Session) enqueuePaneExit(srv *Server, paneID uint32) {
	s.enqueueEvent(paneExitEvent{srv: srv, paneID: paneID})
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

func (s *Session) handleAttachEvent(srv *Server, cc *ClientConn, cols, rows int) attachResult {
	idleSnap := s.snapshotIdleState()
	s.mu.Lock()
	defer s.mu.Unlock()

	cc.cols = cols
	cc.rows = rows

	res := attachResult{}

	if len(s.Windows) == 0 {
		layoutH := rows - render.GlobalBarHeight
		paneH := mux.PaneContentHeight(layoutH)
		pane, err := s.createPane(srv, cols, paneH)
		if err != nil {
			res.err = err
			return res
		}
		winID := s.windowCounter.Add(1)
		w := mux.NewWindow(pane, cols, layoutH)
		w.ID = winID
		w.Name = fmt.Sprintf(WindowNameFormat, winID)
		s.Windows = append(s.Windows, w)
		s.ActiveWindowID = winID
		res.newPane = pane
	}

	s.clients = append(s.clients, cc)
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
