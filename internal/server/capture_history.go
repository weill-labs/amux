package server

import (
	"encoding/json"
	"strings"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type capturePaneTarget struct {
	pane     *mux.Pane
	inWindow bool
	active   bool
	zoomed   bool
}

func (s *Session) resolveCapturePaneTargetForActor(actorPaneID uint32, ref string) (capturePaneTarget, error) {
	return enqueueSessionQuery(s, func(s *Session) (capturePaneTarget, error) {
		pane, w, err := s.resolvePaneAcrossWindowsForActor(actorPaneID, ref)
		if err != nil {
			return capturePaneTarget{}, err
		}
		activeWindow := s.activeWindow()
		return capturePaneTarget{
			pane:     pane,
			inWindow: w != nil,
			active:   activeWindow != nil && activeWindow.ActivePane != nil && activeWindow.ActivePane.ID == pane.ID,
			zoomed:   activeWindow != nil && activeWindow.ZoomedPaneID == pane.ID,
		}, nil
	})
}

func (s *Session) buildServerCapturePane(target capturePaneTarget, req caputil.Request, includeHistory bool) proto.CapturePane {
	textSnap := target.pane.CaptureSnapshot()
	cursor := proto.CaptureCursor{
		Col:    textSnap.CursorCol,
		Row:    textSnap.CursorRow,
		Hidden: textSnap.CursorHidden,
	}
	history := textSnap.History
	content := textSnap.Content
	if req.RewrapSpecified {
		liveHistory := make([]caputil.HistoryLine, len(textSnap.LiveHistory))
		for i, line := range textSnap.LiveHistory {
			liveHistory[i] = caputil.HistoryLine{
				Text:        line.Text,
				SourceWidth: line.SourceWidth,
				Filled:      line.Filled,
			}
		}
		contentRows := make([]caputil.HistoryLine, len(textSnap.ContentRows))
		for i, line := range textSnap.ContentRows {
			contentRows[i] = caputil.HistoryLine{
				Text:        line.Text,
				SourceWidth: textSnap.Width,
				Filled:      line.Filled,
			}
		}
		rewrapped := caputil.RewrapHistoryBuffer(textSnap.BaseHistory, liveHistory, contentRows, cursor, req.RewrapWidth)
		history = rewrapped.History
		content = rewrapped.Content
		cursor = rewrapped.Cursor
	}
	if !includeHistory {
		history = nil
	}

	// Gather fresh CWD for capture (pure getter, no mutation).
	captureCwd, _ := target.pane.DetectCwdBranch()

	capturePane := caputil.BuildPane(caputil.PaneInput{
		ID:         target.pane.ID,
		Name:       target.pane.Meta.Name,
		Active:     target.active,
		Minimized:  target.pane.Meta.Minimized,
		Zoomed:     target.zoomed,
		Host:       target.pane.Meta.Host,
		Task:       target.pane.Meta.Task,
		Color:      target.pane.Meta.Color,
		ConnStatus: target.pane.Meta.Remote,
		Cwd:        captureCwd,
		GitBranch:  target.pane.Meta.GitBranch,
		PR:         target.pane.Meta.PR,
		PRs:        target.pane.Meta.PRs,
		Issues:     target.pane.Meta.Issues,
		Cursor:     cursor,
		Content:    content,
		History:    history,
	}, s.captureAgentStatus([]*mux.Pane{target.pane}))
	if !target.inWindow {
		capturePane.Active = false
		capturePane.Zoomed = false
	}
	return capturePane
}

func (s *Session) capturePaneDirect(args []string, target capturePaneTarget) *Message {
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateScreenRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	capturePane := s.buildServerCapturePane(target, req, false)

	switch {
	case req.FormatJSON:
		out, _ := json.MarshalIndent(capturePane, "", "  ")
		return &Message{Type: MsgTypeCmdResult, CmdOutput: string(out) + "\n"}
	case req.IncludeANSI:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: target.pane.Render() + "\n"}
	default:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: strings.Join(capturePane.Content, "\n") + "\n"}
	}
}

func (s *Session) captureHistory(actorPaneID uint32, args []string) *Message {
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateHistoryRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	target, err := s.resolveCapturePaneTargetForActor(actorPaneID, req.PaneRef)
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	capturePane := s.buildServerCapturePane(target, req, true)

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
