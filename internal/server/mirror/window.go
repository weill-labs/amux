package mirror

import (
	"context"
	"fmt"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

// WindowRef identifies a remote window to mirror: a configured host name, the
// remote session, and the remote window name. Unlike a pane RemoteRef, windows
// are resolved by name on the remote (no numeric ID is carried), so the
// subscription survives the remote reassigning window IDs.
type WindowRef struct {
	Host       string
	Session    string
	WindowName string
}

// windowMirrorState tracks one live window-layout subscription, keyed by the
// local mirror window ID. It carries a single link that streams MsgTypeLayout;
// pane output for the window's panes flows over separate per-pane mirrors.
type windowMirrorState struct {
	localWindowID uint32
	ref           WindowRef
	cols          int
	rows          int
	state         State
	generation    uint64
	link          *remote.Link
	running       bool
	lastErr       string
	// resizePending/resizing coalesce size pushes so the write happens off the
	// caller's goroutine (the session event loop) with at most one writer.
	resizePending bool
	resizing      bool
}

// TrackWindow opens a layout subscription to a remote window. The Manager
// invokes its OnWindowLayout callback with every layout snapshot the remote
// pushes, so the owner can reconcile the local mirror window's structure.
func (m *Manager) TrackWindow(localWindowID uint32, ref WindowRef, cols, rows int) error {
	if m == nil {
		return fmt.Errorf("mirror manager is nil")
	}
	if localWindowID == 0 {
		return fmt.Errorf("local window id is required")
	}
	if ref.Host == "" {
		return fmt.Errorf("remote host is required")
	}
	if ref.WindowName == "" {
		return fmt.Errorf("remote window name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.windowMirrors == nil {
		m.windowMirrors = make(map[uint32]*windowMirrorState)
	}
	ws := &windowMirrorState{
		localWindowID: localWindowID,
		ref:           ref,
		cols:          cols,
		rows:          rows,
		state:         StateConnecting,
	}
	m.windowMirrors[localWindowID] = ws
	if _, ok := m.hosts[ref.Host]; ok {
		m.startWindowLocked(ws)
	} else {
		// Host not configured yet: stay pending so a later Configure can start
		// it (mirrors the per-pane deferred-start path).
		ws.lastErr = fmt.Sprintf("remote host %q is not configured", ref.Host)
	}
	return nil
}

// DetachWindow tears down a window-layout subscription. It does not touch the
// per-pane mirrors of the window's panes; the caller detaches those separately.
func (m *Manager) DetachWindow(localWindowID uint32) {
	if m == nil || localWindowID == 0 {
		return
	}
	m.mu.Lock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil {
		m.mu.Unlock()
		return
	}
	ws.state = StateDetached
	link := ws.link
	ws.link = nil
	delete(m.windowMirrors, localWindowID)
	m.mu.Unlock()
	if link != nil {
		_ = link.Close()
	}
}

// WindowMirrorInfo describes a tracked window mirror for checkpointing.
type WindowMirrorInfo struct {
	Ref  WindowRef
	Cols int
	Rows int
}

// WindowMirrorInfos returns the live window mirrors keyed by local window ID, so
// the owner can persist them for restore after a reload.
func (m *Manager) WindowMirrorInfos() map[uint32]WindowMirrorInfo {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[uint32]WindowMirrorInfo, len(m.windowMirrors))
	for id, ws := range m.windowMirrors {
		if ws == nil || ws.state == StateDetached || ws.state == StateDead {
			continue
		}
		out[id] = WindowMirrorInfo{Ref: ws.ref, Cols: ws.cols, Rows: ws.rows}
	}
	return out
}

// WindowSnapshot reports the current state of a window-layout subscription.
func (m *Manager) WindowSnapshot(localWindowID uint32) (Snapshot, bool) {
	if m == nil {
		return Snapshot{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil {
		return Snapshot{}, false
	}
	return Snapshot{State: ws.state, Generation: ws.generation, LastError: ws.lastErr}, true
}

func (m *Manager) startWindowLocked(ws *windowMirrorState) {
	if ws == nil || ws.running || ws.state == StateDetached || ws.state == StateDead {
		return
	}
	ws.running = true
	id := ws.localWindowID
	m.wg.Add(1)
	go m.runWindow(id, ws)
}

func (m *Manager) runWindow(localWindowID uint32, owner *windowMirrorState) {
	defer m.wg.Done()
	defer m.markWindowStopped(localWindowID, owner)

	attempt := 0
	first := true
	for {
		if err := m.ctx.Err(); err != nil {
			return
		}
		if !m.isCurrentWindow(localWindowID, owner) {
			return
		}
		if !first {
			attempt++
			if attempt > m.retryPolicy.MaxAttempts {
				m.markWindowDead(localWindowID, owner, "remote window connection retry budget exhausted")
				return
			}
			if !m.sleepBeforeRetry(attempt) {
				return
			}
			if !m.isCurrentWindow(localWindowID, owner) {
				return
			}
		}
		first = false

		connected, terminal := m.attachAndReadWindow(localWindowID, owner)
		if terminal {
			return
		}
		if connected {
			attempt = 0
		}
	}
}

func (m *Manager) attachAndReadWindow(localWindowID uint32, owner *windowMirrorState) (connected, terminal bool) {
	host, ref, ok := m.prepareWindowAttempt(localWindowID, owner)
	if !ok {
		return false, true
	}

	attachCtx, cancel := context.WithTimeout(m.ctx, m.attachTimeout)
	defer cancel()

	link := remote.NewLink(host, m.currentDialer())
	if err := link.Connect(attachCtx); err != nil {
		m.recordWindowError(localWindowID, owner, err)
		return false, false
	}
	cols, rows := m.windowSize(localWindowID, owner)
	if err := link.WriteMsg(&proto.Message{
		Type:       proto.MsgTypeAttachWindow,
		Session:    windowSession(host, ref),
		WindowName: ref.WindowName,
		Cols:       cols,
		Rows:       rows,
	}); err != nil {
		_ = link.Close()
		m.recordWindowError(localWindowID, owner, err)
		return false, false
	}

	generation, ok := m.markWindowConnected(localWindowID, owner, link)
	if !ok {
		_ = link.Close()
		return true, true
	}

	err := m.readLoopWindow(localWindowID, owner, generation, link)
	_ = link.Close()
	if err != nil {
		m.recordWindowError(localWindowID, owner, err)
		if m.isWindowTerminal(localWindowID, owner) {
			return true, true
		}
	}
	return true, false
}

func (m *Manager) readLoopWindow(localWindowID uint32, owner *windowMirrorState, generation uint64, link *remote.Link) error {
	for {
		msg, err := link.ReadMsg()
		if err != nil {
			return err
		}
		if msg == nil || msg.Type != proto.MsgTypeLayout || msg.Layout == nil {
			continue
		}
		cb, ref, ok := m.windowLayoutCallback(localWindowID, owner, generation)
		if !ok {
			return nil
		}
		if cb != nil {
			cb(localWindowID, ref, msg.Layout)
		}
	}
}

// ResizeWindow updates the desired size for a mirrored window and pushes it to
// the remote so the remote re-renders the window at the local dimensions. The
// actual link write happens on a dedicated goroutine so a slow remote link can
// never block the caller (the session event loop). The size is also carried on
// the next attach, so a push that lands before the link connects is not lost.
func (m *Manager) ResizeWindow(localWindowID uint32, cols, rows int) {
	if m == nil || localWindowID == 0 || cols <= 0 || rows <= 0 {
		return
	}
	m.mu.Lock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil || (ws.cols == cols && ws.rows == rows) {
		m.mu.Unlock()
		return
	}
	ws.cols = cols
	ws.rows = rows
	ws.resizePending = true
	if ws.resizing {
		// A drainer goroutine is already running; it will pick up the new size.
		m.mu.Unlock()
		return
	}
	ws.resizing = true
	m.wg.Add(1)
	m.mu.Unlock()
	go m.drainWindowResizes(localWindowID, ws)
}

// drainWindowResizes writes pending size pushes to the remote link until none
// remain, off the event loop. At most one drainer runs per window mirror, and
// it always sends the latest requested size.
func (m *Manager) drainWindowResizes(localWindowID uint32, owner *windowMirrorState) {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		ws := m.windowMirrors[localWindowID]
		if ws != owner || !ws.resizePending || ws.state != StateConnected || ws.link == nil {
			if ws == owner {
				ws.resizing = false
			}
			m.mu.Unlock()
			return
		}
		ws.resizePending = false
		cols, rows, link := ws.cols, ws.rows, ws.link
		m.mu.Unlock()

		_ = link.WriteMsg(&proto.Message{Type: proto.MsgTypeResize, Cols: cols, Rows: rows})
	}
}

func (m *Manager) windowSize(localWindowID uint32, owner *windowMirrorState) (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil || ws != owner {
		return 0, 0
	}
	return ws.cols, ws.rows
}

func (m *Manager) prepareWindowAttempt(localWindowID uint32, owner *windowMirrorState) (config.Host, WindowRef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil || ws != owner || ws.state == StateDetached || ws.state == StateDead {
		return config.Host{}, WindowRef{}, false
	}
	ws.state = StateConnecting
	host, ok := m.hosts[ws.ref.Host]
	if !ok {
		ws.lastErr = fmt.Sprintf("remote host %q is not configured", ws.ref.Host)
		return config.Host{}, ws.ref, false
	}
	if host.Session == "" {
		host.Session = ws.ref.Session
	}
	return host, ws.ref, true
}

func (m *Manager) markWindowConnected(localWindowID uint32, owner *windowMirrorState, link *remote.Link) (uint64, bool) {
	m.mu.Lock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil || ws != owner || ws.state == StateDetached || ws.state == StateDead {
		m.mu.Unlock()
		return 0, false
	}
	oldLink := ws.link
	ws.generation++
	gen := ws.generation
	ws.link = link
	ws.state = StateConnected
	ws.lastErr = ""
	m.mu.Unlock()

	if oldLink != nil && oldLink != link {
		_ = oldLink.Close()
	}
	return gen, true
}

func (m *Manager) windowLayoutCallback(localWindowID uint32, owner *windowMirrorState, generation uint64) (func(uint32, WindowRef, *proto.LayoutSnapshot), WindowRef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	if ws == nil || ws != owner || ws.generation != generation || ws.state == StateDetached || ws.state == StateDead {
		return nil, WindowRef{}, false
	}
	return m.onWindowLayout, ws.ref, true
}

func (m *Manager) markWindowStopped(localWindowID uint32, owner *windowMirrorState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ws := m.windowMirrors[localWindowID]; ws != nil && ws == owner {
		ws.running = false
	}
}

func (m *Manager) isCurrentWindow(localWindowID uint32, owner *windowMirrorState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.windowMirrors[localWindowID] == owner
}

func (m *Manager) markWindowDead(localWindowID uint32, owner *windowMirrorState, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ws := m.windowMirrors[localWindowID]; ws != nil && ws == owner {
		ws.state = StateDead
		ws.lastErr = message
	}
}

func (m *Manager) recordWindowError(localWindowID uint32, owner *windowMirrorState, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	if ws != nil && ws == owner && ws.state != StateDead && ws.state != StateDetached {
		ws.state = StateReconnecting
		ws.lastErr = err.Error()
	}
}

func (m *Manager) isWindowTerminal(localWindowID uint32, owner *windowMirrorState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.windowMirrors[localWindowID]
	return ws == nil || ws != owner || ws.state == StateDead || ws.state == StateDetached
}

func windowSession(host config.Host, ref WindowRef) string {
	if ref.Session != "" {
		return ref.Session
	}
	return host.Session
}
