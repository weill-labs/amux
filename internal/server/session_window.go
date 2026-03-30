package server

import (
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
)

// activeWindow returns the currently active window, or nil.
func (s *Session) activeWindow() *mux.Window {
	for _, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			return w
		}
	}
	if len(s.Windows) > 0 {
		return s.Windows[0]
	}
	return nil
}

// findWindowByPaneID returns the window containing the given pane, or nil.
func (s *Session) findWindowByPaneID(paneID uint32) *mux.Window {
	for _, w := range s.Windows {
		if w.Root.FindPane(paneID) != nil {
			return w
		}
	}
	return nil
}

// removeWindow removes a window from the list by ID.
func (s *Session) removeWindow(windowID uint32) {
	for i, w := range s.Windows {
		if w.ID == windowID {
			s.Windows = append(s.Windows[:i], s.Windows[i+1:]...)
			return
		}
	}
}

// nextWindow switches to the next window (wrapping).
func (s *Session) nextWindow() {
	if len(s.Windows) <= 1 {
		return
	}
	for i, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			s.activateWindow(s.Windows[(i+1)%len(s.Windows)])
			return
		}
	}
}

// prevWindow switches to the previous window (wrapping).
func (s *Session) prevWindow() {
	if len(s.Windows) <= 1 {
		return
	}
	for i, w := range s.Windows {
		if w.ID == s.ActiveWindowID {
			prev := (i - 1 + len(s.Windows)) % len(s.Windows)
			s.activateWindow(s.Windows[prev])
			return
		}
	}
}

// resolveWindow finds a window by 1-based index, exact name, or name prefix.
func (s *Session) resolveWindow(ref string) *mux.Window {
	// Try as 1-based index
	if idx, err := strconv.Atoi(ref); err == nil {
		if idx >= 1 && idx <= len(s.Windows) {
			return s.Windows[idx-1]
		}
		return nil
	}
	// Try exact name match
	for _, w := range s.Windows {
		if w.Name == ref {
			return w
		}
	}
	// Try prefix match
	for _, w := range s.Windows {
		if len(ref) > 0 && strings.HasPrefix(w.Name, ref) {
			return w
		}
	}
	return nil
}

// closePaneInWindow removes a pane from its window's layout. If the pane
// is the last one in the window, the window itself is destroyed and focus
// moves to the first remaining window. Returns the name of the closed window,
// or "" if only the pane was removed.
func (s *Session) closePaneInWindow(paneID uint32) string {
	w := s.findWindowByPaneID(paneID)
	if w == nil {
		return ""
	}
	if w.PaneCount() <= 1 {
		wasActive := w.ID == s.ActiveWindowID
		windowName := w.Name
		s.removeWindow(w.ID)
		if wasActive && len(s.Windows) > 0 {
			s.activateWindow(s.Windows[0])
		}
		return windowName
	}
	w.ClosePane(paneID)
	return ""
}

func (s *Session) activateWindow(w *mux.Window) {
	if w == nil {
		return
	}
	s.ActiveWindowID = w.ID
	s.syncWindowSizeToEffectiveClient(w)
}
