package server

import (
	"errors"
	"fmt"
	"runtime/debug"
	"sync/atomic"
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

// MutationContext exposes the subset of session state and helpers that command
// mutation callbacks can use while running on the session event loop.
type MutationContext struct {
	Name             string
	Window           *mux.Window
	Windows          []*mux.Window
	ActiveWindowID   uint32
	PreviousWindowID uint32
	Panes            []*mux.Pane
	RemoteManager    proto.PaneTransport
	generation       *atomic.Uint64
	waiters          *waiterManager
	takenOverPanes   map[uint32]bool

	sess       *Session
	startPanes []*mux.Pane
	closePanes []*mux.Pane
}

func newMutationContext(sess *Session) *MutationContext {
	ctx := &MutationContext{sess: sess}
	ctx.syncFromSession()
	return ctx
}

// syncFromSession refreshes the mirrored fields after helpers or legacy
// commandpkg.Result.Mutate handlers touch Session directly.
func (ctx *MutationContext) syncFromSession() {
	if ctx == nil || ctx.sess == nil {
		return
	}
	ctx.Name = ctx.sess.Name
	ctx.Window = ctx.sess.activeWindow()
	ctx.Windows = ctx.sess.Windows
	ctx.ActiveWindowID = ctx.sess.ActiveWindowID
	ctx.PreviousWindowID = ctx.sess.PreviousWindowID
	ctx.Panes = ctx.sess.Panes
	ctx.RemoteManager = ctx.sess.RemoteManager
	ctx.generation = &ctx.sess.generation
	ctx.waiters = ctx.sess.waiters
	ctx.takenOverPanes = ctx.sess.takenOverPanes
}

// commit copies field-level mutations back to the Session before helpers that
// still operate on *Session run.
func (ctx *MutationContext) commit() {
	if ctx == nil || ctx.sess == nil {
		return
	}
	ctx.sess.Name = ctx.Name
	ctx.sess.Windows = ctx.Windows
	ctx.sess.ActiveWindowID = ctx.ActiveWindowID
	ctx.sess.PreviousWindowID = ctx.PreviousWindowID
	ctx.sess.Panes = ctx.Panes
	ctx.sess.RemoteManager = ctx.RemoteManager
	ctx.sess.takenOverPanes = ctx.takenOverPanes
	ctx.Window = ctx.sess.activeWindow()
}

func (ctx *MutationContext) ScheduleClose(pane *mux.Pane) {
	if pane == nil {
		return
	}
	ctx.closePanes = append(ctx.closePanes, pane)
}

func (ctx *MutationContext) ScheduleStart(pane *mux.Pane) {
	if pane == nil {
		return
	}
	ctx.startPanes = append(ctx.startPanes, pane)
}

func mutationContextCall[T any](ctx *MutationContext, fn func(*Session) (T, error)) (T, error) {
	if ctx == nil || ctx.sess == nil {
		var zero T
		return zero, fmt.Errorf("no session")
	}
	ctx.commit()
	value, err := fn(ctx.sess)
	ctx.syncFromSession()
	return value, err
}

func mutationContextDo(ctx *MutationContext, fn func(*Session) error) error {
	_, err := mutationContextCall(ctx, func(sess *Session) (struct{}, error) {
		return struct{}{}, fn(sess)
	})
	return err
}

func (ctx *MutationContext) activeWindow() *mux.Window {
	w, _ := mutationContextCall(ctx, func(sess *Session) (*mux.Window, error) {
		return sess.activeWindow(), nil
	})
	return w
}

func (ctx *MutationContext) windowForActor(actorPaneID uint32) *mux.Window {
	w, _ := mutationContextCall(ctx, func(sess *Session) (*mux.Window, error) {
		return sess.windowForActor(actorPaneID), nil
	})
	return w
}

func (ctx *MutationContext) resolveWindow(ref string) *mux.Window {
	w, _ := mutationContextCall(ctx, func(sess *Session) (*mux.Window, error) {
		return sess.resolveWindow(ref), nil
	})
	return w
}

func (ctx *MutationContext) activateWindow(w *mux.Window) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.activateWindow(w)
		return nil
	})
}

func (ctx *MutationContext) nextWindow() {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.nextWindow()
		return nil
	})
}

func (ctx *MutationContext) prevWindow() {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.prevWindow()
		return nil
	})
}

func (ctx *MutationContext) lastWindow() bool {
	changed, _ := mutationContextCall(ctx, func(sess *Session) (bool, error) {
		return sess.lastWindow(), nil
	})
	return changed
}

func (ctx *MutationContext) resolvePaneAcrossWindowsForActor(actorPaneID uint32, paneRef string) (*mux.Pane, *mux.Window, error) {
	type resolved struct {
		pane   *mux.Pane
		window *mux.Window
	}
	value, err := mutationContextCall(ctx, func(sess *Session) (resolved, error) {
		pane, window, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
		return resolved{pane: pane, window: window}, err
	})
	if err != nil {
		return nil, nil, err
	}
	return value.pane, value.window, nil
}

func (ctx *MutationContext) resolvePaneWindowForActor(actorPaneID uint32, command string, args []string) (*mux.Pane, *mux.Window, error) {
	type resolved struct {
		pane   *mux.Pane
		window *mux.Window
	}
	value, err := mutationContextCall(ctx, func(sess *Session) (resolved, error) {
		pane, window, err := sess.resolvePaneWindowForActor(actorPaneID, command, args)
		return resolved{pane: pane, window: window}, err
	})
	if err != nil {
		return nil, nil, err
	}
	return value.pane, value.window, nil
}

func (ctx *MutationContext) findPaneByID(id uint32) *mux.Pane {
	pane, _ := mutationContextCall(ctx, func(sess *Session) (*mux.Pane, error) {
		return sess.findPaneByID(id), nil
	})
	return pane
}

func (ctx *MutationContext) findWindowByPaneID(id uint32) *mux.Window {
	w, _ := mutationContextCall(ctx, func(sess *Session) (*mux.Window, error) {
		return sess.findWindowByPaneID(id), nil
	})
	return w
}

func (ctx *MutationContext) createPaneWithMeta(srv *Server, meta mux.PaneMeta, cols, rows int) (*mux.Pane, error) {
	return mutationContextCall(ctx, func(sess *Session) (*mux.Pane, error) {
		return sess.createPaneWithMeta(srv, meta, cols, rows)
	})
}

type pendingLocalPaneResult struct {
	pane  *mux.Pane
	build localPaneBuildRequest
}

func (ctx *MutationContext) preparePendingLocalPane(srv *Server, meta mux.PaneMeta, cols, rows int, colorProfile string) (pendingLocalPaneResult, error) {
	return mutationContextCall(ctx, func(sess *Session) (pendingLocalPaneResult, error) {
		pane, build, err := sess.preparePendingLocalPane(srv, meta, cols, rows, colorProfile)
		if err != nil {
			return pendingLocalPaneResult{}, err
		}
		return pendingLocalPaneResult{pane: pane, build: build}, nil
	})
}

func (ctx *MutationContext) startPendingLocalPaneBuild(srv *Server, placeholder *mux.Pane, req localPaneBuildRequest) {
	if ctx == nil || ctx.sess == nil {
		return
	}
	ctx.sess.startPendingLocalPaneBuild(srv, placeholder, req, nil)
}

func (ctx *MutationContext) beginPaneCleanupKill(pane *mux.Pane, timeout time.Duration) error {
	return mutationContextDo(ctx, func(sess *Session) error {
		return sess.beginPaneCleanupKill(pane, timeout)
	})
}

func (ctx *MutationContext) softClosePane(paneID uint32) paneRemovalResult {
	result, _ := mutationContextCall(ctx, func(sess *Session) (paneRemovalResult, error) {
		return sess.softClosePane(paneID), nil
	})
	return result
}

func (ctx *MutationContext) undoClosePane() (*mux.Pane, error) {
	return mutationContextCall(ctx, func(sess *Session) (*mux.Pane, error) {
		return sess.undoClosePane()
	})
}

func (ctx *MutationContext) replacePaneInstance(oldPane, newPane *mux.Pane, w *mux.Window) error {
	return mutationContextDo(ctx, func(sess *Session) error {
		return sess.replacePaneInstance(oldPane, newPane, w)
	})
}

func (ctx *MutationContext) appendPaneLog(kind string, pane *mux.Pane, reason string) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.appendPaneLog(kind, pane, reason)
		return nil
	})
}

func (ctx *MutationContext) emitEvent(ev Event) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.emitEvent(ev)
		return nil
	})
}

func (ctx *MutationContext) ensureInitialWindowLocked(srv *Server, cols, rows int, preferred *clientConn) (ensureInitialWindowResult, error) {
	return mutationContextCall(ctx, func(sess *Session) (ensureInitialWindowResult, error) {
		return sess.ensureInitialWindowLocked(srv, cols, rows, preferred)
	})
}

func (ctx *MutationContext) insertPreparedPaneIntoActiveWindow(pane *mux.Pane, dir mux.SplitDir, rootLevel, keepFocus bool) error {
	return mutationContextDo(ctx, func(sess *Session) error {
		return sess.insertPreparedPaneIntoActiveWindow(pane, dir, rootLevel, keepFocus)
	})
}

func (ctx *MutationContext) removePane(id uint32) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.removePane(id)
		return nil
	})
}

func (ctx *MutationContext) nextWindowID() uint32 {
	id, _ := mutationContextCall(ctx, func(sess *Session) (uint32, error) {
		return sess.windowCounter.Add(1), nil
	})
	return id
}

func (ctx *MutationContext) finalizePaneRemoval(paneID uint32) paneRemovalResult {
	result, _ := mutationContextCall(ctx, func(sess *Session) (paneRemovalResult, error) {
		return sess.finalizePaneRemoval(paneID), nil
	})
	return result
}

func (ctx *MutationContext) notifyLayoutWaiters(gen uint64) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.notifyLayoutWaiters(gen)
		return nil
	})
}

func (ctx *MutationContext) notifyPaneOutputSubs(paneID uint32) {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.notifyPaneOutputSubs(paneID)
		return nil
	})
}

func (ctx *MutationContext) ensureClientManager() *clientManager {
	manager, _ := mutationContextCall(ctx, func(sess *Session) (*clientManager, error) {
		return sess.ensureClientManager(), nil
	})
	return manager
}

func (ctx *MutationContext) refreshInputTarget() {
	_ = mutationContextDo(ctx, func(sess *Session) error {
		sess.refreshInputTarget()
		return nil
	})
}

type paneHistoryUpdate struct {
	paneID  uint32
	history []proto.StyledLine
}

type paneRender struct {
	paneID uint32
	data   []byte
}

type ensureInitialWindowResult struct {
	layoutChanged bool
	buildDone     chan error
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
	pane, build, err := s.preparePendingLocalPane(srv, mux.PaneMeta{}, cols, paneH, s.paneLaunchColorProfile(preferred))
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
	buildDone := make(chan error, 1)
	s.startPendingLocalPaneBuild(srv, pane, build, buildDone)
	return ensureInitialWindowResult{layoutChanged: true, buildDone: buildDone}, nil
}

type commandMutationEvent struct {
	fn    func(*MutationContext) commandMutationResult
	reply chan commandMutationResult
}

func (e commandMutationEvent) handle(s *Session) {
	ctx := newMutationContext(s)
	res := recoverCommandMutation(e.fn, ctx)
	ctx.commit()
	s.drainScheduledMutationPanes(ctx)
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

func (s *Session) drainScheduledMutationPanes(ctx *MutationContext) {
	for _, pane := range ctx.closePanes {
		s.closePaneAsync(pane)
	}
	for _, pane := range ctx.startPanes {
		pane.Start()
	}
}

// recoverCommandMutation calls fn and converts any panic into an error result
// so the event loop keeps running and the reply channel is always written.
func recoverCommandMutation(fn func(*MutationContext) commandMutationResult, ctx *MutationContext) (res commandMutationResult) {
	defer func() {
		if r := recover(); r != nil {
			res = commandMutationResult{err: ctx.sess.logPanic("command_mutation_panic", r, debug.Stack())}
		}
	}()
	return fn(ctx)
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

func (s *Session) enqueueCommandMutation(fn func(*MutationContext) commandMutationResult) commandMutationResult {
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

// --- emitEvent replaces EventBus.Emit ---

// emitEvent marshals an event and sends it to all matching subscribers.
// Non-blocking: if a subscriber's channel is full the event is dropped.
// Must be called from the session event loop (no mutex needed).
func (s *Session) emitEvent(ev Event) {
	eventloop.Emit(s.eventSubs, ev)
}
