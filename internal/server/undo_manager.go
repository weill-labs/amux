package server

import (
	"fmt"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

// closedPaneRecord holds state for a soft-closed pane awaiting undo or
// final cleanup. The pane's PTY stays alive during the grace period.
type closedPaneRecord struct {
	pane *mux.Pane
}

type undoManager struct {
	pendingCleanupKills map[uint32]*time.Timer
	closedPanes         []closedPaneRecord
	closedPaneTimers    map[uint32]*time.Timer
}

func newUndoManager() *undoManager {
	return &undoManager{
		pendingCleanupKills: make(map[uint32]*time.Timer),
		closedPaneTimers:    make(map[uint32]*time.Timer),
	}
}

func (s *Session) ensureUndoManager() *undoManager {
	if s.undo == nil {
		s.undo = newUndoManager()
	}
	return s.undo
}

func (m *undoManager) removePane(paneID uint32) {
	if timer := m.pendingCleanupKills[paneID]; timer != nil {
		timer.Stop()
		delete(m.pendingCleanupKills, paneID)
	}
	if timer := m.closedPaneTimers[paneID]; timer != nil {
		timer.Stop()
		delete(m.closedPaneTimers, paneID)
	}
}

func (m *undoManager) beginPaneCleanupKill(sess *Session, pane *mux.Pane, timeout time.Duration) error {
	if pane == nil {
		return nil
	}
	if _, exists := m.pendingCleanupKills[pane.ID]; exists {
		return fmt.Errorf("%s cleanup already pending", pane.Meta.Name)
	}
	if err := pane.SignalForegroundProcessGroup(syscall.SIGTERM); err != nil {
		return err
	}
	m.pendingCleanupKills[pane.ID] = time.AfterFunc(timeout, func() {
		sess.enqueuePaneCleanupTimeout(pane.ID)
	})
	return nil
}

func (m *undoManager) trackSoftClosedPane(sess *Session, pane *mux.Pane) {
	if pane == nil {
		return
	}
	m.closedPanes = append(m.closedPanes, closedPaneRecord{pane: pane})
	m.closedPaneTimers[pane.ID] = time.AfterFunc(sess.undoGracePeriod(), func() {
		sess.enqueueUndoExpiry(pane.ID)
	})
}

func (m *undoManager) popClosedPane() (*mux.Pane, error) {
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

func (m *undoManager) finalizeClosedPane(paneID uint32) *mux.Pane {
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

func (m *undoManager) closedPaneCount() int {
	return len(m.closedPanes)
}
