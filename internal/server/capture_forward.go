package server

import (
	"strconv"
	"strings"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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

	snap, err := s.waitForCaptureClient(captureReq, actorPaneID, nil)
	if err != nil {
		if captureReq.FormatJSON {
			return jsonErrorResult("session_shutting_down", err.Error())
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	if snap.client == nil {
		if captureReq.FormatJSON {
			return jsonErrorResult("no_client_attached", "no client attached")
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
	}

	return s.runClientCaptureRequest(args, captureReq, snap.client, s.captureAgentStatus(snap.statusPanes), jsonErrorResult)
}

func (s *Session) waitForCaptureClient(req caputil.Request, actorPaneID uint32, target *capturePaneTarget) (captureClientSnapshot, error) {
	maxRetries := s.captureAttachMaxRetries()
	retryDelay := s.captureAttachRetryDelay()
	for attempt := 0; attempt < maxRetries; attempt++ {
		snap, err := s.captureClientSnapshotForActor(req, actorPaneID, target)
		if err != nil {
			return captureClientSnapshot{}, err
		}
		if snap.client != nil || attempt == maxRetries-1 {
			return snap, nil
		}
		// Client capture can race with hot-reload reattach. A short backoff
		// avoids busy-spinning the actor while giving the interactive client
		// a chance to reconnect and serve the capture request.
		time.Sleep(retryDelay)
	}
	return captureClientSnapshot{}, nil
}

func (s *Session) forwardDebugFramesForActor(actorPaneID uint32) *Message {
	snap, err := s.waitForCaptureClient(caputil.Request{}, actorPaneID, nil)
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	if snap.client == nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "no client attached"}
	}
	return s.runClientCaptureRequest([]string{proto.ClientQueryDebugFramesArg}, caputil.Request{}, snap.client, nil, nil)
}

func (s *Session) captureClientSnapshot(req caputil.Request, target *capturePaneTarget) (captureClientSnapshot, error) {
	return s.captureClientSnapshotForActor(req, 0, target)
}

func (s *Session) captureClientSnapshotForActor(req caputil.Request, actorPaneID uint32, target *capturePaneTarget) (captureClientSnapshot, error) {
	return enqueueSessionQuery(s, func(s *Session) (captureClientSnapshot, error) {
		snap := captureClientSnapshot{}
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
		s.ensureCaptureForwarder().routeResponse(msg, s.sendCaptureRequestAsync)
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

func (s *Session) sendCaptureRequestAsync(req *captureRequest) {
	if req == nil {
		return
	}
	req.client.Send(s.captureRequestMessage(req))
}

func (s *Session) startNextCaptureRequest() {
	s.ensureCaptureForwarder().startNext(s.sendCaptureRequestAsync)
}

func (s *Session) enqueueCaptureRequest(req *captureRequest) error {
	_, err := enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.ensureCaptureForwarder().enqueue(req, s.sendCaptureRequestAsync)
		return struct{}{}, nil
	})
	return err
}

func (s *Session) cancelCaptureRequest(id uint64) {
	_, _ = enqueueSessionQuery(s, func(s *Session) (struct{}, error) {
		s.ensureCaptureForwarder().cancel(id, s.sendCaptureRequestAsync)
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
