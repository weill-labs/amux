package server

import (
	"fmt"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

// takeoverCallback returns the onTakeover callback for panes in this session.
// When a nested amux emits a takeover sequence through the PTY, this handler
// sends the ack, builds proxy panes that route I/O through the existing SSH
// PTY, and splices them into the layout tree — replacing the SSH pane.
func (s *Session) takeoverCallback(srv *Server) func(paneID uint32, req mux.TakeoverRequest) {
	return func(paneID uint32, req mux.TakeoverRequest) {
		s.enqueueTakeover(srv, paneID, req)
	}
}

// handleTakeover processes a takeover request from a nested amux.
// It runs asynchronously (called via goroutine from the readLoop callback).
func (s *Session) handleTakeover(sshPaneID uint32, req mux.TakeoverRequest) {
	type takeoverStart struct {
		sshPane        *mux.Pane
		hostname       string
		managedSession string
	}
	type takeoverLayout struct {
		cols  int
		cellH int
	}

	start, err := enqueueSessionQuery(s, func(s *Session) (takeoverStart, error) {
		if s.takenOverPanes[sshPaneID] {
			return takeoverStart{}, nil
		}
		sshPane := s.findPaneByID(sshPaneID)
		if sshPane == nil {
			return takeoverStart{}, nil
		}
		if s.findWindowByPaneID(sshPaneID) == nil {
			return takeoverStart{}, nil
		}
		s.takenOverPanes[sshPaneID] = true

		hostname := req.Host
		if hostname == "" {
			hostname = "remote"
		}
		return takeoverStart{
			sshPane:        sshPane,
			hostname:       hostname,
			managedSession: managedSessionName(s.Name),
		}, nil
	})
	if err != nil || start.sshPane == nil {
		return
	}
	clearTakeoverPending := func() {
		s.enqueueCommandMutation(func(s *MutationContext) commandMutationResult {
			delete(s.takenOverPanes, sshPaneID)
			return commandMutationResult{}
		})
	}
	failTakeover := func(err error) {
		clearTakeoverPending()
		s.showSessionNotice(formatTakeoverFailureNotice(start.hostname, req.SSHAddress, err))
	}

	// Send ack through the SSH PTY's stdin — carries the agreed session name
	// so the remote amux starts its server at the right socket path.
	if _, err := start.sshPane.Write([]byte(mux.FormatTakeoverAck(start.managedSession) + "\n")); err != nil {
		failTakeover(err)
		return
	}

	layout, err := enqueueSessionQuery(s, func(s *Session) (takeoverLayout, error) {
		w := s.findWindowByPaneID(sshPaneID)
		if w == nil {
			return takeoverLayout{}, fmt.Errorf("pane %d not in any window", sshPaneID)
		}
		cell := w.Root.FindPane(sshPaneID)
		if cell == nil {
			return takeoverLayout{}, fmt.Errorf("pane %d not in layout", sshPaneID)
		}
		return takeoverLayout{cols: cell.W, cellH: cell.H}, nil
	})
	if err != nil {
		failTakeover(err)
		return
	}

	// Build proxy panes for the remote session. If the request has no
	// panes (remote just started), create one default pane.
	remotePanes := req.Panes
	needsInitialResize := len(remotePanes) == 0
	if len(remotePanes) == 0 {
		remotePanes = []mux.TakeoverPane{
			{ID: 1, Name: "pane-1", Cols: layout.cols, Rows: mux.PaneContentHeight(layout.cellH)},
		}
	}

	var proxyPanes []*mux.Pane
	for _, rp := range remotePanes {
		id := s.counter.Add(1)
		meta := mux.PaneMeta{
			Name:   fmt.Sprintf("%s@%s", rp.Name, start.hostname),
			Host:   start.hostname,
			Color:  config.AccentColor(id - 1),
			Remote: string(proto.Connected),
		}

		// writeOverride routes input through the RemoteManager → SSH → remote amux.
		proxyPane := s.ownPane(mux.NewProxyPaneWithScrollback(id, meta, layout.cols, mux.PaneContentHeight(layout.cellH), s.scrollbackLines,
			s.paneOutputCallback(),
			s.paneExitCallback(),
			s.remoteWriteOverride(id),
		))
		proxyPanes = append(proxyPanes, proxyPane)
	}
	removeRemoteMappings := func() {
		if s.RemoteManager == nil {
			return
		}
		for _, pp := range proxyPanes {
			s.RemoteManager.RemovePane(pp.ID)
		}
	}

	// Wire bidirectional I/O: connect back to the remote amux server via SSH
	// and register pane mappings so SendInput/FeedOutput flow correctly.
	// Delay deploy and visible splice until the attach is established so a
	// failed or stale remote session never replaces the raw SSH pane.
	if s.remoteTakeover == nil || req.SSHAddress == "" {
		err := fmt.Errorf("missing SSH takeover connection details")
		if s.remoteTakeover == nil {
			err = fmt.Errorf("remote manager unavailable")
		}
		failTakeover(err)
		return
	}

	paneMappings := make(map[uint32]uint32, len(proxyPanes))
	for i, pp := range proxyPanes {
		paneMappings[pp.ID] = remotePanes[i].ID
	}
	if err := s.remoteTakeover.AttachForTakeover(
		start.hostname, req.SSHAddress, req.SSHUser, req.UID, start.managedSession, paneMappings,
	); err != nil {
		s.logger.Warn("ssh takeover attach failed",
			"event", "ssh_takeover_attach",
			"host", start.hostname,
			"ssh_addr", req.SSHAddress,
			"session", start.managedSession,
			"error", err,
		)
		removeRemoteMappings()
		failTakeover(err)
		return
	}
	if needsInitialResize && len(proxyPanes) > 0 && s.RemoteManager != nil {
		_ = s.RemoteManager.SendResize(proxyPanes[0].ID, layout.cols, mux.PaneContentHeight(layout.cellH))
	}

	// Splice the proxy panes into the layout only after the remote attach has
	// been validated. This keeps the raw SSH pane visible on takeover failure.
	res := s.enqueueCommandMutation(func(s *MutationContext) commandMutationResult {
		w := s.findWindowByPaneID(sshPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("pane %d not in any window", sshPaneID)}
		}
		s.Panes = append(s.Panes, proxyPanes...)
		if _, err := w.SplicePane(sshPaneID, proxyPanes); err != nil {
			for _, pp := range proxyPanes {
				s.removePane(pp.ID)
			}
			return commandMutationResult{err: err}
		}
		if sshPane := s.findPaneByID(sshPaneID); sshPane != nil {
			sshPane.Meta.Dormant = true
		}
		rs := NewRemoteSession(start.hostname, RemoteSessionTakeover)
		rs.PlaceholderPane = sshPaneID
		rs.State = proto.Connected
		for i, pp := range proxyPanes {
			remotePaneID := remotePanes[i].ID
			rs.RemoteToLocal[remotePaneID] = pp.ID
			rs.LocalToRemote[pp.ID] = remotePaneID
		}
		s.sess.remoteSessions[start.hostname] = rs
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		removeRemoteMappings()
		failTakeover(res.err)
		return
	}

	go s.remoteTakeover.DeployToAddress(start.hostname, req.SSHAddress, req.SSHUser)
}
