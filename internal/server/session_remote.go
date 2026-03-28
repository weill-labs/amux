package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

const (
	defaultCaptureAttachMaxRetries = 10
	defaultCaptureAttachRetryDelay = 300 * time.Millisecond
	defaultCaptureResponseTimeout  = 3 * time.Second
)

type captureClientSnapshot struct {
	client      *clientConn
	statusPanes []*mux.Pane
}

// SetupRemoteManager initializes the remote manager with callbacks.
func (s *Session) SetupRemoteManager(cfg *config.Config, buildHash string) {
	mgr := remote.NewManager(cfg, buildHash, remote.ManagerDeps{
		NewHostConn: remote.NewHostConn,
		// onPaneOutput: feed remote output into the proxy pane's emulator
		OnPaneOutput: func(localPaneID uint32, data []byte) {
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
		OnPaneExit: func(localPaneID uint32, reason string) {
			if s.shutdown.Load() {
				return
			}
			s.enqueueRemotePaneExit(localPaneID, reason)
		},
		// onStateChange: update pane metadata when connection state changes
		OnStateChange: func(hostName string, state remote.ConnState) {
			s.enqueueRemoteStateChange(hostName, state)
		},
	})
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
			managedSession: remote.ManagedSessionName(s.Name),
		}, nil
	})
	if err != nil || start.sshPane == nil {
		return
	}
	clearTakeoverPending := func() {
		s.enqueueCommandMutation(func(s *Session) commandMutationResult {
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
	start.sshPane.Write([]byte(mux.FormatTakeoverAck(start.managedSession) + "\n"))

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
			Remote: string(remote.Connected),
		}

		// writeOverride routes input through the RemoteManager → SSH → remote amux.
		proxyPane := s.ownPane(mux.NewProxyPaneWithScrollback(id, meta, layout.cols, mux.PaneContentHeight(layout.cellH), s.scrollbackLines,
			s.paneOutputCallback(),
			s.paneExitCallback(),
			func(data []byte) (int, error) {
				if s.RemoteManager != nil {
					return len(data), s.RemoteManager.SendInput(id, data)
				}
				return len(data), nil
			},
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
	if s.RemoteManager == nil || req.SSHAddress == "" {
		err := fmt.Errorf("missing SSH takeover connection details")
		if s.RemoteManager == nil {
			err = fmt.Errorf("remote manager unavailable")
		}
		failTakeover(err)
		return
	}

	paneMappings := make(map[uint32]uint32, len(proxyPanes))
	for i, pp := range proxyPanes {
		paneMappings[pp.ID] = remotePanes[i].ID
	}
	if err := s.RemoteManager.AttachForTakeover(
		start.hostname, req.SSHAddress, req.SSHUser, req.UID, start.managedSession, paneMappings,
	); err != nil {
		fmt.Fprintf(os.Stderr, "amux: takeover AttachForTakeover: %v\n", err)
		removeRemoteMappings()
		failTakeover(err)
		return
	}
	if needsInitialResize && len(proxyPanes) > 0 {
		_ = s.RemoteManager.SendResize(proxyPanes[0].ID, layout.cols, mux.PaneContentHeight(layout.cellH))
	}

	// Splice the proxy panes into the layout only after the remote attach has
	// been validated. This keeps the raw SSH pane visible on takeover failure.
	res := s.enqueueCommandMutation(func(s *Session) commandMutationResult {
		w := s.findWindowByPaneID(sshPaneID)
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
		removeRemoteMappings()
		failTakeover(res.err)
		return
	}

	go s.RemoteManager.DeployToAddress(start.hostname, req.SSHAddress, req.SSHUser)
}

// forwardCapture sends a capture request to the first attached interactive
// client and waits for its response. The client renders from its own
// emulators — the rendering source of truth. For JSON captures, the server
// gathers agent status (one pgrep call per pane) and includes it in the
// request. The session actor serializes capture dispatch.
func (s *Session) forwardCapture(args []string) *Message {
	return s.forwardCaptureForActor(0, args)
}

func (s *Session) forwardCaptureForActor(actorPaneID uint32, args []string) *Message {
	captureReq := caputil.ParseArgs(args)
	jsonErrorResult := func(code, message string) *Message {
		return &Message{
			Type:      MsgTypeCmdResult,
			CmdOutput: caputil.JSONErrorOutput(captureReq.PaneRef != "", code, message) + "\n",
		}
	}

	// Wait briefly for a client to attach (covers post-reload reconnection).
	var snap captureClientSnapshot
	var err error
	maxRetries := s.captureAttachMaxRetries()
	retryDelay := s.captureAttachRetryDelay()
	for attempt := 0; attempt < maxRetries; attempt++ {
		snap, err = s.captureClientSnapshotForActor(captureReq, actorPaneID, nil)
		if err != nil {
			if captureReq.FormatJSON {
				return jsonErrorResult("session_shutting_down", err.Error())
			}
			return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
		}
		if snap.client != nil {
			break
		}
		if attempt == maxRetries-1 {
			if captureReq.FormatJSON {
				return jsonErrorResult("no_client_attached", "no client attached")
			}
			return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
		}
		// Client capture can race with hot-reload reattach. A short backoff
		// avoids busy-spinning the actor while giving the interactive client
		// a chance to reconnect and serve the capture request.
		time.Sleep(retryDelay)
	}

	return s.runClientCaptureRequest(args, captureReq, snap.client, s.captureAgentStatus(snap.statusPanes), jsonErrorResult)
}

func (s *Session) captureClientSnapshot(req caputil.Request, target *capturePaneTarget) (captureClientSnapshot, error) {
	return s.captureClientSnapshotForActor(req, 0, target)
}

func (s *Session) captureClientSnapshotForActor(req caputil.Request, actorPaneID uint32, target *capturePaneTarget) (captureClientSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (captureClientSnapshot, error) {
		var snap captureClientSnapshot
		for _, cc := range s.ensureClientManager().snapshotClients() {
			if cc.isBootstrapping() {
				continue
			}
			snap.client = cc
			break
		}
		if snap.client == nil || !req.FormatJSON {
			return snap, nil
		}
		if target != nil {
			snap.statusPanes = []*mux.Pane{target.pane}
			return snap, nil
		}
		if req.PaneRef != "" {
			pane, _, err := s.resolvePaneAcrossWindowsForActor(actorPaneID, req.PaneRef)
			if err == nil {
				snap.statusPanes = []*mux.Pane{pane}
			}
			return snap, nil
		}
		if activeWindow := s.activeWindow(); activeWindow != nil {
			snap.statusPanes = append([]*mux.Pane(nil), activeWindow.Panes()...)
		}
		return snap, nil
	})
}

func (s *Session) runClientCaptureRequest(args []string, captureReq caputil.Request, client *clientConn, agentStatus map[uint32]proto.PaneAgentStatus, jsonErrorResult func(string, string) *Message) *Message {
	req := &captureRequest{
		id:          s.ensureCaptureForwarder().nextRequestID(),
		client:      client,
		args:        append([]string(nil), args...),
		agentStatus: agentStatus,
		reply:       make(chan *Message, 1),
	}
	if err := s.enqueueCaptureRequest(req); err != nil {
		if captureReq.FormatJSON && jsonErrorResult != nil {
			return jsonErrorResult("session_shutting_down", err.Error())
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	timer := time.NewTimer(s.captureResponseTimeout())
	defer timer.Stop()
	select {
	case resp := <-req.reply:
		if captureReq.FormatJSON {
			if resp == nil {
				if jsonErrorResult != nil {
					return jsonErrorResult("invalid_capture_response", "capture client returned no response")
				}
				return &Message{Type: MsgTypeCmdResult, CmdErr: "capture client returned no response"}
			}
			if resp.CmdErr != "" {
				return &Message{Type: MsgTypeCmdResult, CmdErr: resp.CmdErr}
			}
			if err := caputil.ValidateJSONOutput(resp.CmdOutput); err != nil {
				if jsonErrorResult != nil {
					return jsonErrorResult("invalid_capture_response", err.Error())
				}
				return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
			}
			return &Message{Type: MsgTypeCmdResult, CmdOutput: ensureTrailingNewline(resp.CmdOutput)}
		}
		return &Message{Type: MsgTypeCmdResult, CmdOutput: resp.CmdOutput, CmdErr: resp.CmdErr}
	case <-timer.C:
		s.cancelCaptureRequest(req.id)
		if captureReq.FormatJSON && jsonErrorResult != nil {
			return jsonErrorResult("capture_timeout", "capture timed out (client unresponsive)")
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: "capture timed out (client unresponsive)"}
	case <-s.sessionEventDone:
		if captureReq.FormatJSON && jsonErrorResult != nil {
			return jsonErrorResult("session_shutting_down", errSessionShuttingDown.Error())
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: errSessionShuttingDown.Error()}
	}
}

func (s *Session) capturePaneWithFallback(actorPaneID uint32, args []string) *Message {
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateScreenRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	target, err := s.resolveCapturePaneTargetForActor(actorPaneID, req.PaneRef)
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	snap, err := s.captureClientSnapshot(req, &target)
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	if snap.client == nil {
		return s.capturePaneDirect(args, target)
	}

	clientReq := req
	clientReq.PaneRef = strconv.FormatUint(uint64(target.pane.ID), 10)
	clientArgs := caputil.ArgsForRequest(clientReq)

	resp := s.runClientCaptureRequest(clientArgs, clientReq, snap.client, s.captureAgentStatus(snap.statusPanes), nil)
	switch resp.CmdErr {
	case "":
		return resp
	case "capture timed out (client unresponsive)":
		return s.capturePaneDirect(args, target)
	default:
		return resp
	}
}

func ensureTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// routeCaptureResponse delivers a capture response from the interactive client
// to the waiting forwardCapture caller. Thread-safe.
func (s *Session) routeCaptureResponse(msg *Message) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.ensureCaptureForwarder().routeResponse(msg, func(req *captureRequest) {
			req.client.Send(s.captureRequestMessage(req))
		})
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
	s.ensureCaptureForwarder().startNext(func(req *captureRequest) {
		req.client.Send(s.captureRequestMessage(req))
	})
}

func (s *Session) enqueueCaptureRequest(req *captureRequest) error {
	_, err := enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.ensureCaptureForwarder().enqueue(req, func(req *captureRequest) {
			req.client.Send(s.captureRequestMessage(req))
		})
		return struct{}{}, nil
	})
	return err
}

func (s *Session) cancelCaptureRequest(id uint64) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.ensureCaptureForwarder().cancel(id, func(req *captureRequest) {
			req.client.Send(s.captureRequestMessage(req))
		})
		return struct{}{}, nil
	})
}

// formatRFC3339Time returns an RFC3339 string for a non-zero time, or "".
func formatRFC3339Time(t time.Time) string {
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
