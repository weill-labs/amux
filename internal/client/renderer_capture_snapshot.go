package client

import (
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type paneRenderSnapshot struct {
	width            int
	height           int
	cursorCol        int
	cursorRow        int
	cursorHidden     bool
	terminal         proto.TerminalState
	rendered         string
	renderedNoCursor string
	scrollback       []paneBufferLine
	screen           []paneBufferLine
	cursorBlockCol   int
	cursorBlockRow   int
	hasCursorBlock   bool
}

type paneScrollbackSnapshotState struct {
	scrollbackLen    int
	scrollbackPushed uint64
	scrollback       []paneBufferLine
}

func capturePaneRenderSnapshot(emu mux.TerminalEmulator, prev paneScrollbackSnapshotState) (paneRenderSnapshot, paneScrollbackSnapshotState) {
	width, height := emu.Size()
	cursorCol, cursorRow := emu.CursorPosition()
	curScrollbackLen := emu.ScrollbackLen()
	curScrollbackPushed := emu.ScrollbackPushed()

	scrollback := prev.scrollback
	rebuildScrollback := prev.scrollback == nil ||
		len(prev.scrollback) != prev.scrollbackLen ||
		curScrollbackPushed < prev.scrollbackPushed
	if !rebuildScrollback {
		appended64 := curScrollbackPushed - prev.scrollbackPushed
		if appended64 > uint64(curScrollbackLen) {
			rebuildScrollback = true
		} else {
			appended := int(appended64)
			trimmed := prev.scrollbackLen + appended - curScrollbackLen
			if trimmed < 0 || trimmed > len(prev.scrollback) {
				rebuildScrollback = true
			} else if appended == 0 && trimmed == 0 {
				scrollback = prev.scrollback
			} else {
				scrollback = prev.scrollback[trimmed:]
				start := curScrollbackLen - appended
				for row := start; row < curScrollbackLen; row++ {
					scrollback = append(scrollback, paneBufferLine{
						text: emu.ScrollbackLineText(row),
					})
				}
			}
		}
	}
	if rebuildScrollback {
		scrollback = make([]paneBufferLine, curScrollbackLen)
		for row := range scrollback {
			scrollback[row] = paneBufferLine{
				text: emu.ScrollbackLineText(row),
			}
		}
	}

	screen := make([]paneBufferLine, height)
	for row := 0; row < height; row++ {
		screen[row] = paneBufferLine{
			text:  emu.ScreenLineText(row),
			cells: captureScreenCells(emu, row, width),
		}
	}

	rendered := emu.Render()
	snap := paneRenderSnapshot{
		width:            width,
		height:           height,
		cursorCol:        cursorCol,
		cursorRow:        cursorRow,
		cursorHidden:     emu.CursorHidden(),
		terminal:         cloneTerminalState(emu.TerminalState()),
		rendered:         rendered,
		renderedNoCursor: rendered,
		scrollback:       scrollback,
		screen:           screen,
	}
	if col, row, ok := emu.CursorBlockPosition(); ok {
		snap.cursorBlockCol = col
		snap.cursorBlockRow = row
		snap.hasCursorBlock = true
		snap.renderedNoCursor = emu.RenderWithoutCursorBlock()
	}
	return snap, paneScrollbackSnapshotState{
		scrollbackLen:    curScrollbackLen,
		scrollbackPushed: curScrollbackPushed,
		scrollback:       scrollback,
	}
}

func cloneTerminalState(state proto.TerminalState) proto.TerminalState {
	state.Palette = append([]color.Color(nil), state.Palette...)
	return state
}

func emptyPaneRenderSnapshot(width, height int) paneRenderSnapshot {
	if height < 0 {
		height = 0
	}
	screen := make([]paneBufferLine, height)
	rendered := strings.Repeat("\n", max(0, height-1))
	return paneRenderSnapshot{
		width:            width,
		height:           height,
		cursorHidden:     true,
		rendered:         rendered,
		renderedNoCursor: rendered,
		screen:           screen,
	}
}

func (s *rendererSnapshot) paneCapture(paneID uint32) (paneRenderSnapshot, bool) {
	if paneID == 0 {
		return paneRenderSnapshot{}, false
	}
	if snap, ok := s.paneCaptures[paneID]; ok {
		return snap, true
	}
	if _, ok := s.paneInfo[paneID]; !ok {
		return paneRenderSnapshot{}, false
	}
	width, height, ok := s.paneDimensions(paneID)
	if !ok {
		return paneRenderSnapshot{}, false
	}
	// Full-session captures are snapshot-only. A pane absent from paneCaptures
	// has no warm emulator in this snapshot, so represent it as blank here rather
	// than entering the actor. Pane-specific captures keep the lazy replay path.
	return emptyPaneRenderSnapshot(width, height), true
}

func (s *rendererSnapshot) captureCompositor() *render.Compositor {
	comp := render.NewCompositor(s.width, s.height, s.sessionName)
	comp.SetWindows(windowInfoFromSnapshot(s.windows, s.activeWinID))
	comp.SetColorProfile(s.colorProfile)
	comp.SetIconSet(s.iconSet)
	return comp
}

func windowInfoFromSnapshot(windows []proto.WindowSnapshot, activeWinID uint32) []render.WindowInfo {
	if len(windows) == 0 {
		return nil
	}
	out := make([]render.WindowInfo, len(windows))
	for i, ws := range windows {
		out[i] = render.WindowInfo{
			Index:    ws.Index,
			Name:     ws.Name,
			IsActive: ws.ID == activeWinID,
			Panes:    len(ws.Panes),
			Zoomed:   ws.Zoomed || ws.ZoomedPaneID != 0,
		}
	}
	return out
}

func (s *rendererSnapshot) paneLookupFromCaptureSnapshot(paneID uint32) render.PaneData {
	info, ok := s.paneInfo[paneID]
	if !ok {
		return nil
	}
	pane, ok := s.paneCapture(paneID)
	if !ok {
		return nil
	}
	return &snapshotPaneData{pane: pane, info: info, caps: s.capabilities}
}

func paneRenderSnapshotLines(lines []paneBufferLine) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.text
	}
	return out
}

func paneRenderSnapshotBuffer(pane paneRenderSnapshot, baseHistory []proto.StyledLine, scrollbackLines int) (scrollback, screen []paneBufferLine) {
	baseStart, liveStart := trimPaneBufferStarts(len(baseHistory), len(pane.scrollback), scrollbackLines)
	scrollback = make([]paneBufferLine, 0, len(baseHistory)-baseStart+len(pane.scrollback)-liveStart)
	for _, line := range baseHistory[baseStart:] {
		scrollback = append(scrollback, paneBufferLine{
			text:  line.Text,
			cells: screenCellsFromProto(line.Cells),
		})
	}
	scrollback = append(scrollback, pane.scrollback[liveStart:]...)
	screen = append([]paneBufferLine(nil), pane.screen...)
	return scrollback, screen
}

type snapshotPaneData struct {
	pane paneRenderSnapshot
	info proto.PaneSnapshot
	caps proto.ClientCapabilities
}

func (p *snapshotPaneData) RenderScreen(active bool) string {
	rendered := p.pane.rendered
	if !active && p.pane.hasCursorBlock {
		rendered = p.pane.renderedNoCursor
	}
	return filterRenderedANSI(rendered, p.caps)
}

func (p *snapshotPaneData) CellAt(col, row int, active bool) render.ScreenCell {
	cell := paneBufferLineCell(p.pane.screen, row, col)
	if !active && p.pane.hasCursorBlock && col == p.pane.cursorBlockCol && row == p.pane.cursorBlockRow {
		stripSnapshotCursorBlock(&cell)
	}
	return cell
}

func stripSnapshotCursorBlock(cell *render.ScreenCell) {
	if cell.Style.Attrs&uv.AttrReverse == 0 {
		return
	}
	if cell.Char != " " && cell.Char != "" {
		return
	}
	cell.Style.Attrs &^= uv.AttrReverse
}

func (p *snapshotPaneData) CopyModeOverlay() *proto.ViewportOverlay { return nil }
func (p *snapshotPaneData) CursorPos() (col, row int) {
	return p.pane.cursorCol, p.pane.cursorRow
}
func (p *snapshotPaneData) CursorHidden() bool { return p.pane.cursorHidden }
func (p *snapshotPaneData) HasCursorBlock() bool {
	return p.pane.hasCursorBlock
}
func (p *snapshotPaneData) ID() uint32   { return p.info.ID }
func (p *snapshotPaneData) Name() string { return p.info.Name }
func (p *snapshotPaneData) TrackedPRs() []proto.TrackedPR {
	return proto.CloneTrackedPRs(p.info.TrackedPRs)
}
func (p *snapshotPaneData) TrackedIssues() []proto.TrackedIssue {
	return proto.CloneTrackedIssues(p.info.TrackedIssues)
}
func (p *snapshotPaneData) Issue() string {
	if p.info.KV == nil {
		return ""
	}
	return p.info.KV["issue"]
}
func (p *snapshotPaneData) Host() string       { return p.info.Host }
func (p *snapshotPaneData) Task() string       { return p.info.Task }
func (p *snapshotPaneData) Color() string      { return p.info.Color }
func (p *snapshotPaneData) Idle() bool         { return p.info.Idle }
func (p *snapshotPaneData) IsLead() bool       { return p.info.Lead }
func (p *snapshotPaneData) ConnStatus() string { return p.info.ConnStatus }
func (p *snapshotPaneData) InCopyMode() bool   { return false }
func (p *snapshotPaneData) CopyModeSearch() string {
	return ""
}

func (p paneRenderSnapshot) ansiString(caps proto.ClientCapabilities) string {
	return filterRenderedANSI(p.rendered, caps)
}

func (p paneRenderSnapshot) textString() string {
	return strings.Join(caputil.TrimOuterBlankRows(paneRenderSnapshotLines(p.screen)), "\n")
}
