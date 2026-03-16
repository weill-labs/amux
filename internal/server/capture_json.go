package server

import (
	"encoding/json"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// paneCapture holds pane data gathered under s.mu for deferred AgentStatus calls.
type paneCapture struct {
	pane *mux.Pane
	cp   proto.CapturePane
}

// captureJSON returns the full-screen JSON capture of the active window.
// Caller does NOT hold s.mu — this method acquires and releases it,
// then calls AgentStatus() outside the lock to avoid blocking the
// server on subprocess calls (pgrep/ps).
func (s *Session) captureJSON() string {
	s.mu.Lock()

	w := s.ActiveWindow()
	if w == nil {
		s.mu.Unlock()
		return "{}"
	}

	// Find the active window's 1-based index.
	windowIndex := 1
	for i, win := range s.Windows {
		if win.ID == s.ActiveWindowID {
			windowIndex = i + 1
			break
		}
	}

	var activePaneID uint32
	if w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}

	root := w.Root
	if w.ZoomedPaneID != 0 {
		root = mux.NewLeafByID(w.ZoomedPaneID, 0, 0, w.Width, w.Height)
	}

	capture := proto.CaptureJSON{
		Session: s.Name,
		Window: proto.CaptureWindow{
			ID:    w.ID,
			Name:  w.Name,
			Index: windowIndex,
		},
		Width:  w.Width,
		Height: w.Height,
	}

	// Gather pane data under the lock (metadata, cursor, content).
	var panes []paneCapture
	root.Walk(func(c *mux.LayoutCell) {
		paneID := c.CellPaneID()
		if paneID == 0 {
			return
		}
		pane := s.findPaneLocked(paneID)
		if pane == nil {
			return
		}

		cp := proto.CapturePane{
			ID:        pane.ID,
			Name:      pane.Meta.Name,
			Active:    pane.ID == activePaneID,
			Minimized: pane.Meta.Minimized,
			Zoomed:    pane.ID == w.ZoomedPaneID,
			Host:      pane.Meta.Host,
			Task:      pane.Meta.Task,
			Color:     pane.Meta.Color,
			Position: &proto.CapturePos{
				X:      c.X,
				Y:      c.Y,
				Width:  c.W,
				Height: c.H,
			},
			Cursor:  captureCursor(pane),
			Content: pane.ContentLines(),
		}
		panes = append(panes, paneCapture{pane: pane, cp: cp})
	})

	s.mu.Unlock()

	// Call AgentStatus() outside the lock — spawns pgrep/ps subprocesses.
	for _, pc := range panes {
		status := pc.pane.AgentStatus()
		pc.cp.Idle = status.Idle
		pc.cp.IdleSince = formatIdleSince(status.IdleSince)
		pc.cp.CurrentCommand = status.CurrentCommand
		pc.cp.ChildPIDs = status.ChildPIDs
		capture.Panes = append(capture.Panes, pc.cp)
	}

	out, _ := json.MarshalIndent(capture, "", "  ")
	return string(out)
}

// capturePaneJSON returns a single pane's JSON capture.
// Caller must hold s.mu. This method releases s.mu before calling
// AgentStatus() to avoid blocking the server on subprocess calls.
func (s *Session) capturePaneJSON(pane *mux.Pane) string {
	var activePaneID uint32
	w := s.ActiveWindow()
	if w != nil && w.ActivePane != nil {
		activePaneID = w.ActivePane.ID
	}

	var zoomedPaneID uint32
	if w != nil {
		zoomedPaneID = w.ZoomedPaneID
	}

	cp := proto.CapturePane{
		ID:        pane.ID,
		Name:      pane.Meta.Name,
		Active:    pane.ID == activePaneID,
		Minimized: pane.Meta.Minimized,
		Zoomed:    pane.ID == zoomedPaneID,
		Host:      pane.Meta.Host,
		Task:      pane.Meta.Task,
		Color:     pane.Meta.Color,
		Cursor:    captureCursor(pane),
		Content:   pane.ContentLines(),
	}

	// Release s.mu before calling AgentStatus() — the caller re-locks
	// after this function returns, but we must not hold the session lock
	// while spawning subprocesses.
	s.mu.Unlock()
	status := pane.AgentStatus()
	s.mu.Lock()

	cp.Idle = status.Idle
	cp.IdleSince = formatIdleSince(status.IdleSince)
	cp.CurrentCommand = status.CurrentCommand
	cp.ChildPIDs = status.ChildPIDs

	out, _ := json.MarshalIndent(cp, "", "  ")
	return string(out)
}

// formatIdleSince returns an RFC3339 string for a non-zero time, or "".
func formatIdleSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// captureCursor reads cursor state from a pane.
func captureCursor(pane *mux.Pane) proto.CaptureCursor {
	col, row := pane.CursorPos()
	return proto.CaptureCursor{
		Col:    col,
		Row:    row,
		Hidden: pane.CursorHidden(),
	}
}

// findPaneLocked finds a pane by ID. Caller must hold s.mu.
func (s *Session) findPaneLocked(id uint32) *mux.Pane {
	for _, p := range s.Panes {
		if p.ID == id {
			return p
		}
	}
	return nil
}
