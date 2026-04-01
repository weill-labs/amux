package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type paneRemovalResult struct {
	pane            *mux.Pane
	paneName        string
	closedWindow    string
	broadcastLayout bool
	sendExit        bool
}

func (s *Session) ownPane(pane *mux.Pane) *mux.Pane {
	if pane == nil {
		return nil
	}
	pane.SetCloseForbiddenOwner(&s.eventLoopOwner)
	return pane
}

func (s *Session) closePaneAsync(pane *mux.Pane) {
	if pane == nil {
		return
	}
	closePane := s.paneCloser
	if closePane == nil {
		closePane = func(pane *mux.Pane) {
			_ = pane.Close()
		}
	}
	go closePane(pane)
}

func cleanupFailedPaneMutation(sess *Session, pane *mux.Pane, err error) commandMutationResult {
	sess.removePane(pane.ID)
	return commandMutationResult{
		err:        err,
		closePanes: []*mux.Pane{pane},
	}
}

// hasPane checks if a pane ID is still in the session's pane list.
func (s *Session) hasPane(id uint32) bool {
	for _, p := range s.Panes {
		if p.ID == id {
			return true
		}
	}
	return false
}

// findPaneByRef searches the flat Panes list for a pane matching the reference.
// This finds panes that may not be in any window's layout tree (e.g., dormant
// SSH takeover panes or orphaned panes from race conditions).
func (s *Session) findPaneByRef(ref string) (*mux.Pane, error) {
	candidates := make([]mux.PaneRefCandidate, 0, len(s.Panes))
	byID := make(map[uint32]*mux.Pane, len(s.Panes))
	for _, pane := range s.Panes {
		candidates = append(candidates, mux.PaneRefCandidate{ID: pane.ID, Name: pane.Meta.Name})
		byID[pane.ID] = pane
	}

	paneID, err := mux.ResolvePaneRef(ref, candidates)
	if err != nil {
		return nil, err
	}
	return byID[paneID], nil
}

// findPaneByID finds a pane by ID.
func (s *Session) findPaneByID(id uint32) *mux.Pane {
	for _, p := range s.Panes {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// removePane removes a pane from the flat list by ID and cleans up its idle timer.
func (s *Session) removePane(id uint32) {
	var pane *mux.Pane
	for i, p := range s.Panes {
		if p.ID == id {
			pane = p
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			break
		}
	}
	s.ensureUndoManager().removePane(id)
	s.ensureInputRouter().removePane(id)
	s.ensureWaiters().removePane(id)
	delete(s.takenOverPanes, id)
	delete(s.terminalEventState, id)
	s.idle.StopTimer(id)
	if s.vtIdle != nil {
		s.vtIdle.StopTimer(id)
	}
	if pane == nil {
		return
	}
	if pane.IsProxy() && s.RemoteManager != nil {
		s.RemoteManager.RemovePane(id)
	}
	s.prunePaneEventSubs(pane.Meta.Name)
}

func (s *Session) prunePaneEventSubs(paneName string) {
	if paneName == "" || len(s.eventSubs) == 0 {
		return
	}
	subs := s.eventSubs[:0]
	for _, sub := range s.eventSubs {
		if sub.Filter.PaneName == paneName {
			continue
		}
		subs = append(subs, sub)
	}
	s.eventSubs = subs
}

func (s *Session) beginPaneCleanupKill(pane *mux.Pane, timeout time.Duration) error {
	return s.ensureUndoManager().beginPaneCleanupKill(s, pane, timeout)
}

func (s *Session) finalizePaneRemoval(paneID uint32) paneRemovalResult {
	pane := s.findPaneByID(paneID)
	if pane == nil {
		return paneRemovalResult{}
	}
	result := paneRemovalResult{
		pane:     pane,
		paneName: pane.Meta.Name,
		sendExit: len(s.Panes) <= 1,
	}
	s.removePane(paneID)
	result.closedWindow = s.closePaneInWindow(paneID)
	if !result.sendExit {
		result.broadcastLayout = true
	}
	return result
}

// softClosePane removes a pane from the layout and pushes it onto the
// closed-pane stack. The PTY stays alive for the grace period, allowing undo.
// Returns a paneRemovalResult describing the layout change.
func (s *Session) softClosePane(paneID uint32) paneRemovalResult {
	pane := s.findPaneByID(paneID)
	if pane == nil {
		return paneRemovalResult{}
	}

	result := paneRemovalResult{
		pane:     pane,
		paneName: pane.Meta.Name,
		sendExit: len(s.Panes) <= 1,
	}

	// If this is the last pane, fall through to hard close (cannot undo).
	if result.sendExit {
		return s.finalizePaneRemoval(paneID)
	}

	// Remove pane from the layout tree.
	result.closedWindow = s.closePaneInWindow(paneID)
	result.broadcastLayout = true

	// Remove from Session.Panes so list/find commands don't see it,
	// but don't call removePane (which would close the PTY).
	for i, p := range s.Panes {
		if p.ID == paneID {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			break
		}
	}
	// Clean up subscriptions and tracking for the now-invisible pane.
	s.ensureWaiters().removePane(paneID)
	delete(s.takenOverPanes, paneID)
	delete(s.terminalEventState, paneID)
	s.idle.StopTimer(paneID)
	if s.vtIdle != nil {
		s.vtIdle.StopTimer(paneID)
	}
	s.prunePaneEventSubs(pane.Meta.Name)

	s.ensureUndoManager().trackSoftClosedPane(s, pane)

	return result
}

// undoClosePane pops the most recently soft-closed pane and re-inserts it
// into the active window's layout.
func (s *Session) undoClosePane() (pane *mux.Pane, err error) {
	pane, err = s.ensureUndoManager().popClosedPane()
	if err != nil {
		return nil, err
	}

	// Re-add to Session.Panes so it's visible again.
	s.Panes = append(s.Panes, pane)

	// Re-insert into the active window.
	w := s.activeWindow()
	if w == nil {
		return nil, fmt.Errorf("no active window")
	}

	if _, err := w.SplitWithOptions(mux.SplitVertical, pane, mux.SplitOptions{
		KeepFocus: w.ZoomedPaneID != 0,
	}); err != nil {
		return nil, err
	}
	return pane, nil
}

func effectiveRespawnDir(pane *mux.Pane) string {
	if pane == nil {
		return ""
	}
	if cwd := pane.LiveCwd(); cwd != "" {
		return cwd
	}
	if cwd, _ := pane.DetectCwdBranch(); cwd != "" {
		return cwd
	}
	return pane.Meta.Dir
}

func (s *Session) replacePaneInstance(oldPane, newPane *mux.Pane, w *mux.Window) error {
	if oldPane == nil || newPane == nil {
		return fmt.Errorf("missing pane")
	}
	if w == nil {
		return fmt.Errorf("pane not in any window")
	}
	replaced := false
	for i, pane := range s.Panes {
		if pane.ID != oldPane.ID {
			continue
		}
		s.Panes[i] = newPane
		replaced = true
		break
	}
	if !replaced {
		return fmt.Errorf("pane %q not found", oldPane.Meta.Name)
	}
	if err := w.ReplacePane(oldPane.ID, newPane); err != nil {
		return err
	}
	delete(s.takenOverPanes, oldPane.ID)
	delete(s.terminalEventState, oldPane.ID)
	s.idle.StopTimer(oldPane.ID)
	if s.vtIdle != nil {
		s.vtIdle.StopTimer(oldPane.ID)
	}
	return nil
}

func (s *Session) respawnPane(srv *Server, pane *mux.Pane, w *mux.Window) (*mux.Pane, error) {
	if pane == nil {
		return nil, fmt.Errorf("missing pane")
	}
	if pane.IsProxy() {
		return nil, fmt.Errorf("cannot respawn proxy pane")
	}

	newPane, err := pane.ReplacementWithColorProfile(s.Name, effectiveRespawnDir(pane), s.paneLaunchColorProfile(nil), s.paneOutputCallback(), s.paneExitCallback())
	if err != nil {
		return nil, err
	}
	newPane = s.ownPane(newPane)
	newPane.SetOnClipboard(s.clipboardCallback())
	newPane.SetOnTakeover(s.takeoverCallback(srv))
	newPane.SetOnMetaUpdate(s.metaCallback())

	if err := s.replacePaneInstance(pane, newPane, w); err != nil {
		return nil, errors.Join(err, newPane.Close())
	}
	pane.SuppressCallbacks()
	return newPane, nil
}

// finalizeClosedPane removes a soft-closed pane from the undo stack and
// returns it for final cleanup (PTY close). The pane was already removed
// from Session.Panes during soft close.
func (s *Session) finalizeClosedPane(paneID uint32) *mux.Pane {
	return s.ensureUndoManager().finalizeClosedPane(paneID)
}

// paneOutputCallback returns the standard onOutput callback for panes.
func (s *Session) paneOutputCallback() func(uint32, []byte, uint64) {
	return func(paneID uint32, data []byte, seq uint64) {
		if s.shutdown.Load() {
			return
		}
		s.enqueuePaneOutput(paneID, data, seq)
	}
}

// paneExitCallback returns the standard onExit callback for panes.
// When the last pane exits, the session sends MsgTypeExit and shuts down.
func (s *Session) paneExitCallback() func(uint32, string) {
	return func(paneID uint32, reason string) {
		if s.shutdown.Load() {
			return
		}
		s.enqueuePaneExit(paneID, reason)
	}
}

// createPane creates a new pane with auto-assigned metadata.
func (s *Session) createPane(srv *Server, cols, rows int) (*mux.Pane, error) {
	return s.createPaneWithMetaForColorProfile(srv, mux.PaneMeta{}, cols, rows, "")
}

func (s *Session) paneLaunchColorProfile(preferred *clientConn) string {
	if preferred != nil && preferred.colorProfileValue() != "" {
		return preferred.colorProfileValue()
	}
	if cc := s.effectiveSizeClient(); cc != nil {
		return cc.colorProfileValue()
	}
	return s.launchColorProfile
}

// createPaneWithMeta creates a new pane with explicit metadata (for spawn).
// Name, Host, and Color are auto-assigned if empty.
func (s *Session) createPaneWithMeta(srv *Server, meta mux.PaneMeta, cols, rows int) (*mux.Pane, error) {
	return s.createPaneWithMetaForColorProfile(srv, meta, cols, rows, "")
}

func (s *Session) createPaneWithMetaForColorProfile(srv *Server, meta mux.PaneMeta, cols, rows int, colorProfile string) (*mux.Pane, error) {
	id := s.counter.Add(1)
	if meta.Name == "" {
		meta.Name = fmt.Sprintf(mux.PaneNameFormat, id)
	}
	if meta.Host == "" {
		meta.Host = mux.DefaultHost
	}
	if meta.Color == "" {
		meta.Color = config.AccentColor(id - 1)
	}

	if colorProfile == "" {
		colorProfile = s.paneLaunchColorProfile(nil)
	}

	pane, err := mux.NewPaneWithScrollbackColorProfile(id, meta, cols, rows, s.Name, s.scrollbackLines, colorProfile,
		s.paneOutputCallback(),
		s.paneExitCallback(),
	)
	if err != nil {
		return nil, err
	}
	pane = s.ownPane(pane)

	pane.SetOnClipboard(s.clipboardCallback())
	pane.SetOnTakeover(s.takeoverCallback(srv))
	pane.SetOnMetaUpdate(s.metaCallback())

	s.Panes = append(s.Panes, pane)
	s.appendPaneLog(paneLogEventCreate, pane, "")
	s.logPaneCreate(pane, "local")
	return pane, nil
}

// prepareRemotePane creates and connects a proxy pane that routes I/O to a
// remote host, but does not register it in session state or any window.
// Caller must run this outside the session event loop (the remote manager
// needs to make SSH calls).
func (s *Session) prepareRemotePane(hostName string, cols, rows int) (*mux.Pane, error) {
	if s.RemoteManager == nil {
		return nil, fmt.Errorf("no remote hosts configured for host %q", hostName)
	}

	id := s.counter.Add(1)
	meta := mux.PaneMeta{
		Name:   fmt.Sprintf(mux.PaneNameFormat, id),
		Host:   hostName,
		Color:  s.remotePaneColor(hostName),
		Remote: string(proto.Connected), // initial state
	}

	// Create the proxy pane with a writeOverride that routes to the remote manager
	pane := s.ownPane(mux.NewProxyPaneWithScrollback(id, meta, cols, rows, s.scrollbackLines,
		s.paneOutputCallback(),
		s.paneExitCallback(),
		s.remoteWriteOverride(id),
	))

	// Create the corresponding pane on the remote server
	_, err := s.RemoteManager.CreatePane(hostName, id, s.Name)
	if err != nil {
		s.RemoteManager.RemovePane(id)
		return nil, err
	}

	return pane, nil
}

// insertPreparedPaneIntoActiveWindow registers a pre-created pane in the
// session and inserts it into the active window layout.
func (s *Session) insertPreparedPaneIntoActiveWindow(pane *mux.Pane, dir mux.SplitDir, rootLevel, keepFocus bool) error {
	w := s.activeWindow()
	if w == nil {
		return fmt.Errorf("no window")
	}

	s.Panes = append(s.Panes, pane)
	s.logPaneCreate(pane, "remote")
	opts := mux.SplitOptions{KeepFocus: keepFocus || w.ZoomedPaneID != 0}
	var err error
	if rootLevel {
		_, err = w.SplitRootWithOptions(dir, pane, opts)
	} else {
		_, err = w.SplitWithOptions(dir, pane, opts)
	}
	if err != nil {
		s.removePane(pane.ID)
		return err
	}
	return nil
}
