package server

import (
	"fmt"
	"os"
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
			pane, err := enqueueSessionQuery(s, func(s *Session) (*mux.Pane, error) {
				return s.findPaneByID(localPaneID), nil
			})
			if err != nil {
				return
			}
			if pane != nil {
				pane.FeedOutput(data)
			}
		},
		// onPaneExit: clean up when a remote pane exits
		func(localPaneID uint32) {
			if s.shutdown.Load() {
				return
			}
			s.enqueueRemotePaneExit(localPaneID)
		},
		// onStateChange: update pane metadata when connection state changes
		func(hostName string, state remote.ConnState) {
			s.enqueueRemoteStateChange(hostName, state)
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
		s.enqueueTakeover(srv, paneID, req)
	}
}

// handleTakeover processes a takeover request from a nested amux.
// It runs asynchronously (called via goroutine from the readLoop callback).
func (s *Session) handleTakeover(srv *Server, sshPaneID uint32, req mux.TakeoverRequest) {
	type takeoverStart struct {
		sshPane  *mux.Pane
		hostname string
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
		if s.FindWindowByPaneID(sshPaneID) == nil {
			return takeoverStart{}, nil
		}
		s.takenOverPanes[sshPaneID] = true

		hostname := req.Host
		if hostname == "" {
			hostname = "remote"
		}
		return takeoverStart{sshPane: sshPane, hostname: hostname}, nil
	})
	if err != nil || start.sshPane == nil {
		return
	}

	// Send ack through the SSH PTY's stdin — carries the agreed session name
	// so the remote amux starts its server at the right socket path.
	start.sshPane.Write([]byte(mux.FormatTakeoverAck(req.Session) + "\n"))

	layout, err := enqueueSessionQuery(s, func(s *Session) (takeoverLayout, error) {
		w := s.FindWindowByPaneID(sshPaneID)
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
			Color:  config.CatppuccinMocha[(id-1)%uint32(len(config.CatppuccinMocha))],
			Remote: string(remote.Connected),
		}

		// writeOverride routes input through the RemoteManager → SSH → remote amux.
		proxyPane := mux.NewProxyPane(id, meta, layout.cols, mux.PaneContentHeight(layout.cellH),
			s.paneOutputCallback(),
			s.paneExitCallback(),
			func(data []byte) (int, error) {
				if s.RemoteManager != nil {
					return len(data), s.RemoteManager.SendInput(id, data)
				}
				return len(data), nil
			},
		)
		proxyPanes = append(proxyPanes, proxyPane)
	}

	// Splice the proxy panes into the layout, replacing the SSH pane
	res := s.enqueueCommandMutation(func(s *Session) commandMutationResult {
		w := s.FindWindowByPaneID(sshPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("pane %d not in any window", sshPaneID)}
		}
		for _, pp := range proxyPanes {
			s.Panes = append(s.Panes, pp)
		}
		if _, err := w.SplicePane(sshPaneID, proxyPanes); err != nil {
			for _, pp := range proxyPanes {
				s.removePane(pp.ID)
			}
			return commandMutationResult{err: err}
		}
		if sshPane := s.findPaneByID(sshPaneID); sshPane != nil {
			sshPane.Meta.Dormant = true
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err != nil {
		return
	}
	if res.broadcastLayout {
		s.broadcastLayout()
	}

	// Wire bidirectional I/O: connect back to the remote amux server via SSH
	// and register pane mappings so SendInput/FeedOutput flow correctly.
	// Also deploy the updated binary so the remote amux hot-reloads.
	if s.RemoteManager != nil && req.SSHAddress != "" {
		go s.RemoteManager.DeployToAddress(start.hostname, req.SSHAddress, req.SSHUser)

		paneMappings := make(map[uint32]uint32, len(proxyPanes))
		for i, pp := range proxyPanes {
			paneMappings[pp.ID] = remotePanes[i].ID
		}
		if err := s.RemoteManager.AttachForTakeover(
			start.hostname, req.SSHAddress, req.SSHUser, req.UID, req.Session, paneMappings,
		); err != nil {
			fmt.Fprintf(os.Stderr, "amux: takeover AttachForTakeover: %v\n", err)
		} else if needsInitialResize && len(proxyPanes) > 0 {
			_ = s.RemoteManager.SendResize(proxyPanes[0].ID, cols, mux.PaneContentHeight(cellH))
		}
	}
}

// forwardCapture sends a capture request to the first attached interactive
// client and waits for its response. The client renders from its own
// emulators — the rendering source of truth. For JSON captures, the server
// gathers agent status (one pgrep call per pane) and includes it in the
// request. The session actor serializes capture dispatch.
func (s *Session) forwardCapture(args []string) *Message {
	type captureSnapshot struct {
		client      *ClientConn
		statusPanes []*mux.Pane
	}

	// Wait briefly for a client to attach (covers post-reload reconnection).
	const maxRetries = 10
	var snap captureSnapshot
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		snap, err = enqueueSessionQuery(s, func(s *Session) (captureSnapshot, error) {
			if len(s.clients) == 0 {
				return captureSnapshot{}, nil
			}
			snap := captureSnapshot{client: s.clients[0]}
			if slices.Contains(args, "json") {
				snap.statusPanes = append([]*mux.Pane(nil), s.Panes...)
			}
			return snap, nil
		})
		if err != nil {
			return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
		}
		if snap.client != nil {
			break
		}
		if attempt == maxRetries-1 {
			return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Gather agent status. Call AgentStatus() for each pane, then use
	// cached idleState to stabilize the result: when both the idle timer
	// and pgrep agree the pane is idle, use the server's cached timestamp
	// and shell name. This avoids pgrep false positives from transient
	// shell children under parallel load, while still trusting pgrep for
	// busy panes (including silent long-running processes like sleep).
	var agentStatus map[uint32]proto.PaneAgentStatus
	if len(snap.statusPanes) > 0 {
		_, sinceSnap := s.snapshotIdleFull()

		agentStatus = make(map[uint32]proto.PaneAgentStatus, len(snap.statusPanes))
		for _, p := range snap.statusPanes {
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

	req := &captureRequest{
		id:          s.captureCounter.Add(1),
		client:      snap.client,
		args:        append([]string(nil), args...),
		agentStatus: agentStatus,
		reply:       make(chan *Message, 1),
	}
	if err := s.enqueueCaptureRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case resp := <-req.reply:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: resp.CmdOutput, CmdErr: resp.CmdErr}
	case <-timer.C:
		s.cancelCaptureRequest(req.id)
		return &Message{Type: MsgTypeCmdResult, CmdErr: "capture timed out (client unresponsive)"}
	case <-s.sessionEventDone:
		return &Message{Type: MsgTypeCmdResult, CmdErr: errSessionShuttingDown.Error()}
	}
}

// routeCaptureResponse delivers a capture response from the interactive client
// to the waiting forwardCapture caller. Thread-safe.
func (s *Session) routeCaptureResponse(msg *Message) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		if s.captureCurrent == nil {
			return struct{}{}, nil
		}
		req := s.captureCurrent
		s.captureCurrent = nil
		select {
		case req.reply <- msg:
		default:
		}
		s.startNextCaptureRequest()
		return struct{}{}, nil
	})
}

func (s *Session) captureRequestMessage(req *captureRequest) *Message {
	return &Message{
		Type:        MsgTypeCaptureRequest,
		CmdArgs:     req.args,
		AgentStatus: req.agentStatus,
	}
}

func (s *Session) startNextCaptureRequest() {
	if s.captureCurrent != nil || len(s.captureQueue) == 0 {
		return
	}
	next := s.captureQueue[0]
	s.captureQueue = s.captureQueue[1:]
	s.captureCurrent = next
	next.client.Send(s.captureRequestMessage(next))
}

func (s *Session) enqueueCaptureRequest(req *captureRequest) error {
	_, err := enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		if s.captureCurrent == nil {
			s.captureCurrent = req
			req.client.Send(s.captureRequestMessage(req))
			return struct{}{}, nil
		}
		s.captureQueue = append(s.captureQueue, req)
		return struct{}{}, nil
	})
	return err
}

func (s *Session) cancelCaptureRequest(id uint64) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		if s.captureCurrent != nil && s.captureCurrent.id == id {
			s.captureCurrent = nil
			s.startNextCaptureRequest()
			return struct{}{}, nil
		}
		for i, req := range s.captureQueue {
			if req.id != id {
				continue
			}
			s.captureQueue = append(s.captureQueue[:i], s.captureQueue[i+1:]...)
			break
		}
		return struct{}{}, nil
	})
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
