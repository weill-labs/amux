package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/remote"
)

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
func (s *Session) findPaneByRef(ref string) *mux.Pane {
	// Exact match by name or numeric ID
	for _, p := range s.Panes {
		if p.Meta.Name == ref || strconv.FormatUint(uint64(p.ID), 10) == ref {
			return p
		}
	}
	// Prefix match
	for _, p := range s.Panes {
		if strings.HasPrefix(p.Meta.Name, ref) {
			return p
		}
	}
	return nil
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
	for i, p := range s.Panes {
		if p.ID == id {
			s.Panes = append(s.Panes[:i], s.Panes[i+1:]...)
			break
		}
	}
	if queue := s.pacedPanes[id]; queue != nil {
		queue.close()
		delete(s.pacedPanes, id)
	}
	s.idle.StopTimer(id)
}

func (s *Session) enqueuePacedPaneInput(pane *mux.Pane, chunks []encodedKeyChunk) error {
	queue, err := enqueueSessionQuery(s, func(sess *Session) (*pacedInputQueue, error) {
		if !sess.hasPane(pane.ID) {
			return nil, fmt.Errorf("%s not found", pane.Meta.Name)
		}
		return sess.pacedPaneQueue(pane), nil
	})
	if err != nil {
		return err
	}
	return queue.enqueue(chunks)
}

func (s *Session) pacedPaneQueue(pane *mux.Pane) *pacedInputQueue {
	if s.pacedPanes == nil {
		s.pacedPanes = make(map[uint32]*pacedInputQueue)
	}
	if queue := s.pacedPanes[pane.ID]; queue != nil {
		return queue
	}
	queue := newPacedInputQueue("pane "+pane.Meta.Name, func(data []byte) error {
		_, err := pane.Write(data)
		return err
	})
	s.pacedPanes[pane.ID] = queue
	return queue
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
func (s *Session) paneExitCallback() func(uint32) {
	return func(paneID uint32) {
		if s.shutdown.Load() {
			return
		}
		s.enqueuePaneExit(paneID)
	}
}

// createPane creates a new pane with auto-assigned metadata.
func (s *Session) createPane(srv *Server, cols, rows int) (*mux.Pane, error) {
	return s.createPaneWithMeta(srv, mux.PaneMeta{}, cols, rows)
}

// createPaneWithMeta creates a new pane with explicit metadata (for spawn).
// Name, Host, and Color are auto-assigned if empty.
func (s *Session) createPaneWithMeta(srv *Server, meta mux.PaneMeta, cols, rows int) (*mux.Pane, error) {
	id := s.counter.Add(1)
	if meta.Name == "" {
		meta.Name = fmt.Sprintf(mux.PaneNameFormat, id)
	}
	if meta.Host == "" {
		meta.Host = mux.DefaultHost
	}
	if meta.Color == "" {
		meta.Color = config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))]
	}

	pane, err := mux.NewPaneWithScrollback(id, meta, cols, rows, s.Name, s.scrollbackLines,
		s.paneOutputCallback(),
		s.paneExitCallback(),
	)
	if err != nil {
		return nil, err
	}

	pane.SetOnClipboard(s.clipboardCallback())
	pane.SetOnTakeover(s.takeoverCallback(srv))

	s.Panes = append(s.Panes, pane)
	return pane, nil
}

// prepareRemotePane creates and connects a proxy pane that routes I/O to a
// remote host, but does not register it in session state or any window.
// Caller must NOT hold s.mu (the remote manager needs to make SSH calls).
func (s *Session) prepareRemotePane(srv *Server, hostName string, cols, rows int) (*mux.Pane, error) {
	if s.RemoteManager == nil {
		return nil, fmt.Errorf("no remote hosts configured")
	}

	id := s.counter.Add(1)
	meta := mux.PaneMeta{
		Name:   fmt.Sprintf(mux.PaneNameFormat, id),
		Host:   hostName,
		Color:  s.RemoteManager.Config().HostColor(hostName),
		Remote: string(remote.Connected), // initial state
	}

	// Create the proxy pane with a writeOverride that routes to the remote manager
	pane := mux.NewProxyPaneWithScrollback(id, meta, cols, rows, s.scrollbackLines,
		s.paneOutputCallback(),
		s.paneExitCallback(),
		func(data []byte) (int, error) {
			return len(data), s.RemoteManager.SendInput(id, data)
		},
	)

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
func (s *Session) insertPreparedPaneIntoActiveWindow(pane *mux.Pane, dir mux.SplitDir, rootLevel bool) error {
	w := s.ActiveWindow()
	if w == nil {
		return fmt.Errorf("no window")
	}

	s.Panes = append(s.Panes, pane)
	var err error
	if rootLevel {
		_, err = w.SplitRoot(dir, pane)
	} else {
		_, err = w.Split(dir, pane)
	}
	if err != nil {
		s.removePane(pane.ID)
		if s.RemoteManager != nil && pane.IsProxy() {
			s.RemoteManager.RemovePane(pane.ID)
		}
		return err
	}
	return nil
}
