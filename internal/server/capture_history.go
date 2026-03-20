package server

import (
	"encoding/json"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type captureHistoryArgs struct {
	includeANSI bool
	colorMap    bool
	formatJSON  bool
	displayMode bool
	historyMode bool
	paneRef     string
}

func parseCaptureArgs(args []string) captureHistoryArgs {
	var req captureHistoryArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ansi":
			req.includeANSI = true
		case "--colors":
			req.colorMap = true
		case "--display":
			req.displayMode = true
		case "--history":
			req.historyMode = true
		case "--format":
			if i+1 < len(args) && args[i+1] == "json" {
				req.formatJSON = true
				i++
			}
		default:
			req.paneRef = args[i]
		}
	}
	return req
}

func (s *Session) captureHistory(cc *ClientConn, args []string) *Message {
	req := parseCaptureArgs(args)
	if !req.historyMode {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "internal error: captureHistory called without --history"}
	}
	if req.includeANSI || req.colorMap || req.displayMode {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "--history is mutually exclusive with --ansi, --colors, and --display"}
	}
	if req.paneRef == "" {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "--history requires a pane target"}
	}

	type historySnapshot struct {
		pane     *mux.Pane
		inWindow bool
		active   bool
		zoomed   bool
	}
	snap, err := enqueueSessionQuery(s, func(s *Session) (historySnapshot, error) {
		pane, w, err := cc.resolvePaneAcrossWindowsLocked(s, req.paneRef)
		if err != nil {
			return historySnapshot{}, err
		}
		activeWindow := s.ActiveWindow()
		return historySnapshot{
			pane:     pane,
			inWindow: w != nil,
			active:   activeWindow != nil && activeWindow.ActivePane != nil && activeWindow.ActivePane.ID == pane.ID,
			zoomed:   activeWindow != nil && activeWindow.ZoomedPaneID == pane.ID,
		}, nil
	})
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	pane := snap.pane
	textSnap := pane.CaptureSnapshot()

	capturePane := proto.CapturePane{
		ID:         pane.ID,
		Name:       pane.Meta.Name,
		Active:     snap.active,
		Minimized:  pane.Meta.Minimized,
		Zoomed:     snap.zoomed,
		Host:       pane.Meta.Host,
		Task:       pane.Meta.Task,
		Color:      pane.Meta.Color,
		ConnStatus: pane.Meta.Remote,
		Cursor: proto.CaptureCursor{
			Col:    textSnap.CursorCol,
			Row:    textSnap.CursorRow,
			Hidden: textSnap.CursorHidden,
		},
		Content: textSnap.Content,
		History: textSnap.History,
	}
	if !snap.inWindow {
		capturePane.Active = false
		capturePane.Zoomed = false
	}
	capturePane.ApplyAgentStatus(s.captureAgentStatus([]*mux.Pane{pane}))

	if req.formatJSON {
		out, _ := json.MarshalIndent(capturePane, "", "  ")
		return &Message{Type: MsgTypeCmdResult, CmdOutput: string(out) + "\n"}
	}

	lines := append(append([]string{}, capturePane.History...), capturePane.Content...)
	return &Message{Type: MsgTypeCmdResult, CmdOutput: strings.Join(lines, "\n") + "\n"}
}

func (s *Session) captureAgentStatus(panes []*mux.Pane) map[uint32]proto.PaneAgentStatus {
	if len(panes) == 0 {
		return nil
	}

	_, sinceSnap := s.snapshotIdleFull()
	agentStatus := make(map[uint32]proto.PaneAgentStatus, len(panes))
	for _, p := range panes {
		st := p.AgentStatus()
		pas := proto.PaneAgentStatus{
			Idle:           st.Idle,
			IdleSince:      formatIdleSince(st.IdleSince),
			CurrentCommand: st.CurrentCommand,
			ChildPIDs:      nonNilPIDs(st.ChildPIDs),
		}
		if st.Idle {
			if t, ok := sinceSnap[p.ID]; ok {
				pas.IdleSince = formatIdleSince(t)
			}
			pas.CurrentCommand = p.ShellName()
			pas.ChildPIDs = []int{}
		}
		agentStatus[p.ID] = pas
	}
	return agentStatus
}
