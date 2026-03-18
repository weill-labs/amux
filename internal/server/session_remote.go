package server

import (
	"fmt"
	"slices"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

// SetupRemoteManager initializes the remote manager with callbacks.
func (s *Session) SetupRemoteManager(cfg *config.Config, buildHash string) {
	mgr := remote.NewManager(cfg, buildHash)
	mgr.SetCallbacks(
		// onPaneOutput: feed remote output into the proxy pane's emulator
		func(localPaneID uint32, data []byte) {
			s.mu.Lock()
			pane := s.findPaneLocked(localPaneID)
			s.mu.Unlock()
			if pane != nil {
				pane.FeedOutput(data)
			}
		},
		// onPaneExit: clean up when a remote pane exits
		func(localPaneID uint32) {
			if s.shutdown.Load() {
				return
			}
			s.mu.Lock()
			if !s.hasPane(localPaneID) {
				s.mu.Unlock()
				return
			}
			s.removePane(localPaneID)
			s.closePaneInWindow(localPaneID)
			s.mu.Unlock()
			s.broadcastLayout()
		},
		// onStateChange: update pane metadata when connection state changes
		func(hostName string, state remote.ConnState) {
			s.mu.Lock()
			for _, p := range s.Panes {
				if p.Meta.Host == hostName && p.IsProxy() {
					p.Meta.Remote = string(state)
				}
			}
			s.mu.Unlock()
			s.broadcastLayout()
		},
	)
	s.RemoteManager = mgr
}

// takeoverCallback returns the onTakeover callback for panes in this session.
// When a nested amux emits a takeover sequence through the PTY, this handler
// sends the ack, builds proxy panes that route I/O through the existing SSH
// PTY, and splices them into the layout tree — replacing the SSH pane.
func (s *Session) takeoverCallback(srv *Server) func(paneID uint32, req mux.TakeoverRequest) {
	return func(paneID uint32, req mux.TakeoverRequest) {
		go s.handleTakeover(srv, paneID, req)
	}
}

// handleTakeover processes a takeover request from a nested amux.
// It runs asynchronously (called via goroutine from the readLoop callback).
func (s *Session) handleTakeover(srv *Server, sshPaneID uint32, req mux.TakeoverRequest) {
	s.mu.Lock()

	// Guard against duplicate takeover for the same pane (e.g., the remote
	// emits the sequence twice during reconnect).
	if s.takenOverPanes[sshPaneID] {
		s.mu.Unlock()
		return
	}
	s.takenOverPanes[sshPaneID] = true

	sshPane := s.findPaneLocked(sshPaneID)
	if sshPane == nil {
		s.mu.Unlock()
		return
	}

	// Verify the SSH pane is still in a window's layout
	w := s.FindWindowByPaneID(sshPaneID)
	if w == nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	// Send ack through the SSH PTY's stdin — this tells the remote amux
	// to enter managed mode instead of launching its own TUI.
	sshPane.Write([]byte(mux.TakeoverAck))

	// Wait briefly for the remote server to start
	time.Sleep(500 * time.Millisecond)

	hostname := req.Host
	if hostname == "" {
		hostname = "remote"
	}

	// Re-acquire lock and read fresh cell dimensions (may have changed
	// during the unlocked period due to resize events).
	s.mu.Lock()
	w = s.FindWindowByPaneID(sshPaneID)
	if w == nil {
		s.mu.Unlock()
		return
	}
	cell := w.Root.FindPane(sshPaneID)
	if cell == nil {
		s.mu.Unlock()
		return
	}
	cols, cellH := cell.W, cell.H

	// Build proxy panes for the remote session. If the request has no
	// panes (remote just started), create one default pane.
	remotePanes := req.Panes
	if len(remotePanes) == 0 {
		remotePanes = []mux.TakeoverPane{
			{ID: 1, Name: "pane-1", Cols: cols, Rows: mux.PaneContentHeight(cellH)},
		}
	}

	var proxyPanes []*mux.Pane
	for _, rp := range remotePanes {
		id := s.counter.Add(1)
		meta := mux.PaneMeta{
			Name:   fmt.Sprintf("%s@%s", rp.Name, hostname),
			Host:   hostname,
			Color:  config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
			Remote: string(remote.Connected),
		}

		// The writeOverride routes input through the original SSH PTY.
		// All proxy panes share the same SSH connection (the original PTY),
		// but the remote amux server demuxes by pane ID.
		proxyPane := mux.NewProxyPane(id, meta, cols, mux.PaneContentHeight(cellH),
			s.paneOutputCallback(),
			s.paneExitCallback(srv),
			func(data []byte) (int, error) {
				return sshPane.Write(data)
			},
		)
		proxyPanes = append(proxyPanes, proxyPane)
	}

	// Splice the proxy panes into the layout, replacing the SSH pane
	for _, pp := range proxyPanes {
		s.Panes = append(s.Panes, pp)
	}
	_, spliceErr := w.SplicePane(sshPaneID, proxyPanes)
	if spliceErr != nil {
		for _, pp := range proxyPanes {
			s.removePane(pp.ID)
		}
		s.mu.Unlock()
		return
	}

	// The SSH pane stays in the panes list (dormant) — its PTY maintains
	// the SSH connection for unsplice fallback.
	sshPane.Meta.Dormant = true
	s.mu.Unlock()

	s.broadcastLayout()

	// Deploy updated binary in background — the remote amux hot-reloads
	// via its file watcher when the binary changes on disk.
	if s.RemoteManager != nil && req.SSHAddress != "" {
		go s.RemoteManager.DeployToAddress(hostname, req.SSHAddress, req.SSHUser)
	}
}

// forwardCapture sends a capture request to the first attached interactive
// client and waits for its response. The client renders from its own
// emulators — the rendering source of truth. For JSON captures, the server
// gathers agent status (one pgrep call per pane) and includes it in the
// request. Serialized via captureMu so concurrent callers don't clobber.
func (s *Session) forwardCapture(args []string) *Message {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()

	// Wait briefly for a client to attach (covers post-reload reconnection).
	// The loop acquires s.mu; on success it remains held through the snapshot below.
	const maxRetries = 10
	var client *ClientConn
	for attempt := range maxRetries {
		s.mu.Lock()
		if len(s.clients) > 0 {
			// Use the first attached client. In practice there's one interactive
			// client at a time; if multiple attach, the first is authoritative.
			client = s.clients[0]
			break
		}
		s.mu.Unlock()
		if attempt == maxRetries-1 {
			return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
		}
		time.Sleep(300 * time.Millisecond)
	}

	ch := make(chan *Message, 1)
	s.captureResult = ch

	// For JSON captures, snapshot pane list while holding the lock.
	var statusPanes []*mux.Pane
	if slices.Contains(args, "json") {
		statusPanes = make([]*mux.Pane, len(s.Panes))
		copy(statusPanes, s.Panes)
	}
	s.mu.Unlock()

	// Gather agent status. Call AgentStatus() for each pane, then use
	// cached idleState to stabilize the result: when both the idle timer
	// and pgrep agree the pane is idle, use the server's cached timestamp
	// and shell name. This avoids pgrep false positives from transient
	// shell children under parallel load, while still trusting pgrep for
	// busy panes (including silent long-running processes like sleep).
	var agentStatus map[uint32]proto.PaneAgentStatus
	if len(statusPanes) > 0 {
		_, sinceSnap := s.snapshotIdleFull()

		agentStatus = make(map[uint32]proto.PaneAgentStatus, len(statusPanes))
		for _, p := range statusPanes {
			st := p.AgentStatus()
			pas := proto.PaneAgentStatus{
				Idle:           st.Idle,
				IdleSince:      formatIdleSince(st.IdleSince),
				CurrentCommand: st.CurrentCommand,
				ChildPIDs:      nonNilPIDs(st.ChildPIDs),
			}
			// When pgrep confirms idle, use cached timestamp to avoid
			// race where pgrep sees idle but idleSince was just reset.
			if st.Idle {
				if t, ok := sinceSnap[p.ID]; ok {
					pas.IdleSince = formatIdleSince(t)
				}
				pas.CurrentCommand = p.ShellName()
				pas.ChildPIDs = []int{}
			}
			agentStatus[p.ID] = pas
		}
	}

	client.Send(&Message{
		Type:        MsgTypeCaptureRequest,
		CmdArgs:     args,
		AgentStatus: agentStatus,
	})

	defer func() {
		s.mu.Lock()
		s.captureResult = nil
		s.mu.Unlock()
	}()

	select {
	case resp := <-ch:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: resp.CmdOutput, CmdErr: resp.CmdErr}
	case <-time.After(3 * time.Second):
		return &Message{Type: MsgTypeCmdResult, CmdErr: "capture timed out (client unresponsive)"}
	}
}

// routeCaptureResponse delivers a capture response from the interactive client
// to the waiting forwardCapture caller. Thread-safe.
func (s *Session) routeCaptureResponse(msg *Message) {
	s.mu.Lock()
	ch := s.captureResult
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// formatIdleSince returns an RFC3339 string for a non-zero time, or "".
func formatIdleSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// nonNilPIDs ensures a nil slice becomes an empty slice for JSON marshaling.
func nonNilPIDs(pids []int) []int {
	if pids == nil {
		return []int{}
	}
	return pids
}
