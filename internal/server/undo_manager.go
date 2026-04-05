package server

import (
	"fmt"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

// closedPaneRecord holds state for a soft-closed pane awaiting undo or
// final cleanup. The pane's PTY stays alive during the grace period.
type closedPaneRecord struct {
	pane *mux.Pane
}

type undoTimer interface {
	Stop() bool
}

type undoAfterFunc func(time.Duration, func()) undoTimer

type undoManagerConfig struct {
	gracePeriod time.Duration
	afterFunc   undoAfterFunc
}

// UndoManager owns soft-close state, grace-period timers, and undo cleanup
// event handling for a session.
type UndoManager struct {
	gracePeriod         time.Duration
	afterFunc           undoAfterFunc
	pendingCleanupKills map[uint32]undoTimer
	closedPanes         []closedPaneRecord
	closedPaneTimers    map[uint32]undoTimer
}

func newUndoManager(cfg undoManagerConfig) *UndoManager {
	if cfg.gracePeriod == 0 {
		cfg.gracePeriod = config.UndoGracePeriod
	}
	if cfg.afterFunc == nil {
		cfg.afterFunc = func(delay time.Duration, fn func()) undoTimer {
			return time.AfterFunc(delay, fn)
		}
	}
	return &UndoManager{
		gracePeriod:         cfg.gracePeriod,
		afterFunc:           cfg.afterFunc,
		pendingCleanupKills: make(map[uint32]undoTimer),
		closedPaneTimers:    make(map[uint32]undoTimer),
	}
}

func (s *Session) ensureUndoManager() *UndoManager {
	if s.undo == nil {
		s.undo = newUndoManager(undoManagerConfig{})
	}
	return s.undo
}

func (m *UndoManager) removePane(paneID uint32) {
	if timer := m.pendingCleanupKills[paneID]; timer != nil {
		timer.Stop()
		delete(m.pendingCleanupKills, paneID)
	}
	if timer := m.closedPaneTimers[paneID]; timer != nil {
		timer.Stop()
		delete(m.closedPaneTimers, paneID)
	}
}

func (m *UndoManager) beginPaneCleanupKill(pane *mux.Pane, timeout time.Duration, enqueueTimeout func(uint32)) error {
	if pane == nil {
		return nil
	}
	if _, exists := m.pendingCleanupKills[pane.ID]; exists {
		return fmt.Errorf("%s cleanup already pending", pane.Meta.Name)
	}
	if err := pane.SignalForegroundProcessGroup(syscall.SIGTERM); err != nil {
		return err
	}
	m.pendingCleanupKills[pane.ID] = m.afterFunc(timeout, func() {
		enqueueTimeout(pane.ID)
	})
	return nil
}

func (m *UndoManager) trackSoftClosedPane(pane *mux.Pane, enqueueExpiry func(uint32)) {
	if pane == nil {
		return
	}
	m.closedPanes = append(m.closedPanes, closedPaneRecord{pane: pane})
	m.closedPaneTimers[pane.ID] = m.afterFunc(m.gracePeriod, func() {
		enqueueExpiry(pane.ID)
	})
}

func (m *UndoManager) popClosedPane() (*mux.Pane, error) {
	if len(m.closedPanes) == 0 {
		return nil, fmt.Errorf("no closed pane to undo")
	}

	idx := len(m.closedPanes) - 1
	rec := m.closedPanes[idx]
	m.closedPanes = m.closedPanes[:idx]
	if timer := m.closedPaneTimers[rec.pane.ID]; timer != nil {
		timer.Stop()
		delete(m.closedPaneTimers, rec.pane.ID)
	}
	return rec.pane, nil
}

func (m *UndoManager) finalizeClosedPane(paneID uint32) *mux.Pane {
	for i, rec := range m.closedPanes {
		if rec.pane.ID == paneID {
			m.closedPanes = append(m.closedPanes[:i], m.closedPanes[i+1:]...)
			if timer := m.closedPaneTimers[paneID]; timer != nil {
				timer.Stop()
				delete(m.closedPaneTimers, paneID)
			}
			return rec.pane
		}
	}
	return nil
}

func (m *UndoManager) closedPaneCount() int {
	return len(m.closedPanes)
}

func (m *UndoManager) closeFinalizedPane(paneID uint32, closePane func(*mux.Pane)) bool {
	pane := m.finalizeClosedPane(paneID)
	if pane == nil {
		return false
	}
	closePane(pane)
	return true
}

func (m *UndoManager) handlePaneExit(paneID uint32, closePane func(*mux.Pane)) bool {
	return m.closeFinalizedPane(paneID, closePane)
}

func (m *UndoManager) handlePaneCleanupTimeout(paneID uint32, findPane func(uint32) *mux.Pane, signal func(*mux.Pane) error, finalize func(uint32, bool, string)) {
	pane := findPane(paneID)
	if pane == nil {
		return
	}
	_ = signal(pane)
	finalize(paneID, true, "cleanup timeout")
}

func (m *UndoManager) handleUndoExpiry(paneID uint32, closePane func(*mux.Pane)) {
	m.closeFinalizedPane(paneID, closePane)
}

type paneCleanupTimeoutEvent struct {
	paneID uint32
}

func (e paneCleanupTimeoutEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.ensureUndoManager().handlePaneCleanupTimeout(
		e.paneID,
		s.findPaneByID,
		func(pane *mux.Pane) error {
			return pane.SignalForegroundProcessGroup(syscall.SIGKILL)
		},
		s.handleFinalizedPaneRemoval,
	)
}

type undoExpiryEvent struct {
	paneID uint32
}

func (e undoExpiryEvent) handle(s *Session) {
	if s.shutdown.Load() {
		return
	}
	s.ensureUndoManager().handleUndoExpiry(e.paneID, s.closePaneAsync)
}

func (s *Session) enqueueUndoExpiry(paneID uint32) {
	s.enqueueEvent(undoExpiryEvent{paneID: paneID})
}

func (s *Session) enqueuePaneCleanupTimeout(paneID uint32) {
	s.enqueueEvent(paneCleanupTimeoutEvent{paneID: paneID})
}
