package server

import (
	"encoding/json"
	"strings"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func (s *Session) captureHistory(cc *ClientConn, args []string) *Message {
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateHistoryRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	type historySnapshot struct {
		pane     *mux.Pane
		inWindow bool
		active   bool
		zoomed   bool
	}
	snap, err := enqueueSessionQuery(s, func(s *Session) (historySnapshot, error) {
		pane, w, err := cc.resolvePaneAcrossWindowsLocked(s, req.PaneRef)
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

	// Gather fresh CWD for capture (pure getter, no mutation)
	captureCwd, _ := pane.DetectCwdBranch()

	capturePane := caputil.BuildPane(caputil.PaneInput{
		ID:         pane.ID,
		Name:       pane.Meta.Name,
		Active:     snap.active,
		Minimized:  pane.Meta.Minimized,
		Zoomed:     snap.zoomed,
		Host:       pane.Meta.Host,
		Task:       pane.Meta.Task,
		Color:      pane.Meta.Color,
		ConnStatus: pane.Meta.Remote,
		Cwd:        captureCwd,
		GitBranch:  pane.Meta.GitBranch,
		PR:         pane.Meta.PR,
		Cursor: proto.CaptureCursor{
			Col:    textSnap.CursorCol,
			Row:    textSnap.CursorRow,
			Hidden: textSnap.CursorHidden,
		},
		Content: textSnap.Content,
		History: textSnap.History,
	}, s.captureAgentStatus([]*mux.Pane{pane}))
	if !snap.inWindow {
		capturePane.Active = false
		capturePane.Zoomed = false
	}

	if req.FormatJSON {
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
