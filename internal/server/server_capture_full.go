package server

import (
	"encoding/json"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type serverFullSessionCapture struct {
	session      string
	notice       string
	width        int
	height       int
	root         *mux.LayoutCell
	activePaneID uint32
	zoomedPaneID uint32
	window       proto.CaptureWindow
	windows      []render.WindowInfo
	panes        []*serverCapturePane
	panesByID    map[uint32]*serverCapturePane
}

type serverCapturePane struct {
	pane     *mux.Pane
	info     proto.PaneSnapshot
	position proto.CapturePos
	render   mux.PaneRenderSnapshot
}

func (s serverFullSessionCapture) lookup(paneID uint32) render.PaneData {
	pane := s.panesByID[paneID]
	if pane == nil {
		return nil
	}
	return &serverPaneData{pane: pane}
}

func (s serverFullSessionCapture) agentStatusPanes() []*mux.Pane {
	panes := make([]*mux.Pane, 0, len(s.panes))
	for _, pane := range s.panes {
		if pane.pane != nil {
			panes = append(panes, pane.pane)
		}
	}
	return panes
}

func (s serverFullSessionCapture) compositor() *render.Compositor {
	comp := render.NewCompositor(s.width, s.height, s.session)
	comp.SetWindows(s.windows)
	return comp
}

func (s serverFullSessionCapture) renderANSI() string {
	return s.compositor().RenderFullWithOverlay(s.root, s.activePaneID, s.lookup, render.OverlayState{Message: s.notice}, false)
}

func (s serverFullSessionCapture) buildJSON(req caputil.Request, agentStatus map[uint32]proto.PaneAgentStatus) proto.CaptureJSON {
	capture := proto.CaptureJSON{
		Session: s.session,
		Window:  s.window,
		Width:   s.width,
		Height:  s.height,
		Notice:  s.notice,
		Panes:   make([]proto.CapturePane, 0, len(s.panes)),
	}
	includeHistory := req.HistoryMode && req.FormatJSON
	for _, pane := range s.panes {
		content := append([]string(nil), pane.render.Content...)
		var history []string
		if includeHistory {
			history = append([]string(nil), pane.render.History...)
		}
		cp := caputil.BuildPane(caputil.PaneInput{
			ID:            pane.info.ID,
			Name:          pane.info.Name,
			Active:        pane.info.ID == s.activePaneID,
			Lead:          pane.info.Lead,
			Zoomed:        pane.info.ID == s.zoomedPaneID,
			Host:          pane.info.Host,
			Task:          pane.info.Task,
			Color:         pane.info.Color,
			ColumnIndex:   pane.info.ColumnIndex,
			ConnStatus:    pane.info.ConnStatus,
			GitBranch:     pane.info.GitBranch,
			PR:            pane.info.PR,
			KV:            pane.info.KV,
			TrackedPRs:    pane.info.TrackedPRs,
			TrackedIssues: pane.info.TrackedIssues,
			Cursor: caputil.CursorFromState(
				pane.render.CursorCol,
				pane.render.CursorRow,
				pane.render.CursorHidden,
				pane.render.Terminal,
			),
			Terminal: caputil.TerminalFromState(pane.render.Terminal),
			Content:  content,
			History:  history,
		}, agentStatus)
		cp.Position = &proto.CapturePos{
			X:      pane.position.X,
			Y:      pane.position.Y,
			Width:  pane.position.Width,
			Height: pane.position.Height,
		}
		capture.Panes = append(capture.Panes, cp)
	}
	return capture
}

type serverPaneData struct {
	pane *serverCapturePane
}

func (p *serverPaneData) RenderScreen(active bool) string {
	if !active && p.pane.render.HasCursorBlock {
		return p.pane.render.RenderedNoCursor
	}
	return p.pane.render.Rendered
}

func (p *serverPaneData) CellAt(col, row int, active bool) render.ScreenCell {
	cell, ok := p.pane.render.CellAt(col, row)
	sc := render.CellFromUVValue(cell, ok)
	if !active && p.pane.render.HasCursorBlock && col == p.pane.render.CursorBlockCol && row == p.pane.render.CursorBlockRow {
		stripServerSnapshotCursorBlock(&sc)
	}
	return sc
}

func stripServerSnapshotCursorBlock(cell *render.ScreenCell) {
	if cell.Style.Attrs&uv.AttrReverse == 0 {
		return
	}
	if cell.Char != " " && cell.Char != "" {
		return
	}
	cell.Style.Attrs &^= uv.AttrReverse
}

func (p *serverPaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *serverPaneData) CursorPos() (col, row int) {
	return p.pane.render.CursorCol, p.pane.render.CursorRow
}
func (p *serverPaneData) CursorHidden() bool { return p.pane.render.CursorHidden }
func (p *serverPaneData) HasCursorBlock() bool {
	return p.pane.render.HasCursorBlock
}
func (p *serverPaneData) ID() uint32   { return p.pane.info.ID }
func (p *serverPaneData) Name() string { return p.pane.info.Name }
func (p *serverPaneData) TrackedPRs() []proto.TrackedPR {
	return proto.CloneTrackedPRs(p.pane.info.TrackedPRs)
}
func (p *serverPaneData) TrackedIssues() []proto.TrackedIssue {
	return proto.CloneTrackedIssues(p.pane.info.TrackedIssues)
}
func (p *serverPaneData) Issue() string {
	if p.pane.info.KV == nil {
		return ""
	}
	return p.pane.info.KV["issue"]
}
func (p *serverPaneData) Host() string           { return p.pane.info.Host }
func (p *serverPaneData) Task() string           { return p.pane.info.Task }
func (p *serverPaneData) Color() string          { return p.pane.info.Color }
func (p *serverPaneData) Idle() bool             { return p.pane.info.Idle }
func (p *serverPaneData) IsLead() bool           { return p.pane.info.Lead }
func (p *serverPaneData) ConnStatus() string     { return p.pane.info.ConnStatus }
func (p *serverPaneData) InCopyMode() bool       { return false }
func (p *serverPaneData) CopyModeSearch() string { return "" }

func (s *Session) captureFullSessionSnapshot() (serverFullSessionCapture, error) {
	return enqueueSessionQueryLegacy(s.context(), s, func(s *Session) (serverFullSessionCapture, error) {
		w := s.activeWindow()
		if w == nil || w.Root == nil {
			return serverFullSessionCapture{}, fmt.Errorf("no window")
		}

		windowIndex := 1
		for i, candidate := range s.Windows {
			if candidate != nil && candidate.ID == w.ID {
				windowIndex = i + 1
				break
			}
		}
		windowSnap := w.SnapshotWindow(windowIndex)
		idleSnap := s.snapshotIdleState()
		paneInfo := make(map[uint32]proto.PaneSnapshot, len(windowSnap.Panes))
		for _, pane := range windowSnap.Panes {
			pane.Idle = idleSnap[pane.ID]
			paneInfo[pane.ID] = pane
		}

		paneByID := make(map[uint32]*mux.Pane, len(windowSnap.Panes))
		for _, pane := range w.Panes() {
			paneByID[pane.ID] = pane
		}

		root := mux.RebuildLayout(windowSnap.Root)
		if w.ZoomedPaneID != 0 {
			root = mux.NewLeafByID(w.ZoomedPaneID, 0, 0, w.Width, w.Height)
		}

		activePaneID := windowSnap.ActivePaneID
		panesByID := make(map[uint32]*serverCapturePane)
		var panes []*serverCapturePane
		root.Walk(func(cell *mux.LayoutCell) {
			paneID := cell.CellPaneID()
			if paneID == 0 {
				return
			}
			pane := paneByID[paneID]
			info, ok := paneInfo[paneID]
			if pane == nil || !ok {
				return
			}
			capturePane := &serverCapturePane{
				pane: pane,
				info: info,
				position: proto.CapturePos{
					X:      cell.X,
					Y:      cell.Y,
					Width:  cell.W,
					Height: cell.H,
				},
				render: pane.CaptureRenderSnapshot(),
			}
			panes = append(panes, capturePane)
			panesByID[paneID] = capturePane
		})

		return serverFullSessionCapture{
			session:      s.Name,
			notice:       s.notice,
			width:        w.Width,
			height:       w.Height + render.GlobalBarHeight,
			root:         root,
			activePaneID: activePaneID,
			zoomedPaneID: windowSnap.ZoomedPaneID,
			window: proto.CaptureWindow{
				ID:     windowSnap.ID,
				Name:   windowSnap.Name,
				Index:  windowSnap.Index,
				Zoomed: windowSnap.Zoomed || windowSnap.ZoomedPaneID != 0,
			},
			windows:   serverWindowInfo(s.Windows, s.ActiveWindowID),
			panes:     panes,
			panesByID: panesByID,
		}, nil
	})
}

func serverWindowInfo(windows []*mux.Window, activeWindowID uint32) []render.WindowInfo {
	if len(windows) == 0 {
		return nil
	}
	out := make([]render.WindowInfo, 0, len(windows))
	for i, window := range windows {
		if window == nil {
			continue
		}
		out = append(out, render.WindowInfo{
			Index:    i + 1,
			Name:     window.Name,
			IsActive: window.ID == activeWindowID,
			Panes:    window.PaneCount(),
			Zoomed:   window.ZoomedPaneID != 0,
		})
	}
	return out
}

func (s *Session) captureFullSessionDirect(args []string) *Message {
	req := caputil.ParseArgs(args)
	if err := caputil.ValidateScreenRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	if req.HistoryMode && !req.FormatJSON {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "--history requires a pane target"}
	}

	snap, err := s.captureFullSessionSnapshot()
	if err != nil {
		if req.FormatJSON {
			return &Message{Type: MsgTypeCmdResult, CmdOutput: caputil.JSONErrorOutput(false, "state_unavailable", err.Error()) + "\n"}
		}
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}

	switch {
	case req.FormatJSON:
		capture := snap.buildJSON(req, s.captureAgentStatus(snap.agentStatusPanes()))
		out, _ := json.MarshalIndent(capture, "", "  ")
		return &Message{Type: MsgTypeCmdResult, CmdOutput: string(out) + "\n"}
	case req.ColorMap:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: render.ExtractColorMap(snap.renderANSI(), snap.width, snap.height) + "\n"}
	case req.IncludeANSI:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: snap.renderANSI()}
	default:
		return &Message{Type: MsgTypeCmdResult, CmdOutput: render.MaterializeGrid(snap.renderANSI(), snap.width, snap.height)}
	}
}

func captureFullSessionLocally(ctx *CommandContext, args []string) *Message {
	if ctx == nil || ctx.Sess == nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "no session"}
	}
	return ctx.Sess.captureFullSessionDirect(args)
}
