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

type paneRenderSnapshotState struct {
	scrollbackLen    int
	scrollbackPushed uint64
	scrollback       []paneBufferLine
	screenWidth      int
	screenHeight     int
	screen           []paneBufferLine
	renderWidth      int
	renderHeight     int
	rendered         string
	renderedNoCursor string
	renderedValid    bool
	cursorBlockCol   int
	cursorBlockRow   int
	hasCursorBlock   bool
}

func capturePaneRenderSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState) (paneRenderSnapshot, paneRenderSnapshotState, bool) {
	width, height := emu.Size()
	cursorCol, cursorRow := emu.CursorPosition()
	scrollbackState := captureScrollbackSnapshot(emu, prev)
	screenState, screenChanged := captureScreenSnapshot(emu, prev, width, height)

	cursorBlockCol, cursorBlockRow, hasCursorBlock := emu.CursorBlockPosition()
	rendered, renderedNoCursor := captureRenderedSnapshot(emu, prev, width, height, screenChanged, cursorBlockCol, cursorBlockRow, hasCursorBlock)
	snap := paneRenderSnapshot{
		width:            width,
		height:           height,
		cursorCol:        cursorCol,
		cursorRow:        cursorRow,
		cursorHidden:     emu.CursorHidden(),
		terminal:         cloneTerminalState(emu.TerminalState()),
		rendered:         rendered,
		renderedNoCursor: renderedNoCursor,
		scrollback:       scrollbackState.scrollback,
		screen:           screenState.screen,
	}
	if hasCursorBlock {
		snap.cursorBlockCol = cursorBlockCol
		snap.cursorBlockRow = cursorBlockRow
		snap.hasCursorBlock = true
	}
	state := mergePaneSnapshotState(scrollbackState, screenState)
	state.renderWidth = width
	state.renderHeight = height
	state.rendered = rendered
	state.renderedNoCursor = renderedNoCursor
	state.renderedValid = true
	state.cursorBlockCol = cursorBlockCol
	state.cursorBlockRow = cursorBlockRow
	state.hasCursorBlock = hasCursorBlock
	return snap, state, screenChanged
}

func captureRenderedSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState, width, height int, screenChanged bool, cursorBlockCol, cursorBlockRow int, hasCursorBlock bool) (rendered, renderedNoCursor string) {
	renderCacheValid := prev.renderedValid && prev.renderWidth == width && prev.renderHeight == height
	cursorBlockChanged := prev.hasCursorBlock != hasCursorBlock ||
		(hasCursorBlock && (prev.cursorBlockCol != cursorBlockCol || prev.cursorBlockRow != cursorBlockRow))
	if !renderCacheValid || screenChanged || cursorBlockChanged {
		rendered = emu.Render()
		renderedNoCursor = rendered
		if hasCursorBlock {
			renderedNoCursor = renderWithoutCursorBlockSnapshot(emu)
		}
		return rendered, renderedNoCursor
	}

	rendered = prev.rendered
	if !hasCursorBlock {
		return rendered, rendered
	}
	return rendered, prev.renderedNoCursor
}

func renderWithoutCursorBlockSnapshot(emu mux.TerminalEmulator) string {
	rendered := emu.RenderWithoutCursorBlock()
	// RenderWithoutCursorBlock temporarily edits emulator cells. Clear that
	// internal touched state so the next snapshot only sees real PTY changes.
	emu.DrainScreenChanges()
	return rendered
}

func captureScrollbackSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState) paneRenderSnapshotState {
	curLen := emu.ScrollbackLen()
	curPushed := emu.ScrollbackPushed()
	scrollback, ok := extendScrollbackSnapshot(emu, prev, curLen, curPushed)
	if !ok {
		scrollback = rebuildScrollbackSnapshot(emu, curLen)
	}
	return paneRenderSnapshotState{
		scrollbackLen:    curLen,
		scrollbackPushed: curPushed,
		scrollback:       scrollback,
	}
}

func extendScrollbackSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState, curLen int, curPushed uint64) ([]paneBufferLine, bool) {
	if prev.scrollback == nil || len(prev.scrollback) != prev.scrollbackLen || curPushed < prev.scrollbackPushed {
		return nil, false
	}
	appended64 := curPushed - prev.scrollbackPushed
	if appended64 > uint64(curLen) {
		return nil, false
	}
	appended := int(appended64)
	trimmed := prev.scrollbackLen + appended - curLen
	if trimmed < 0 || trimmed > len(prev.scrollback) {
		return nil, false
	}
	if appended == 0 && trimmed == 0 {
		return prev.scrollback, true
	}

	scrollback := prev.scrollback[trimmed:]
	start := curLen - appended
	for row := start; row < curLen; row++ {
		scrollback = append(scrollback, paneBufferLine{
			text: emu.ScrollbackLineText(row),
		})
	}
	return scrollback, true
}

func rebuildScrollbackSnapshot(emu mux.TerminalEmulator, scrollbackLen int) []paneBufferLine {
	scrollback := make([]paneBufferLine, scrollbackLen)
	for row := range scrollback {
		scrollback[row] = paneBufferLine{
			text: emu.ScrollbackLineText(row),
		}
	}
	return scrollback
}

func captureScreenSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState, width, height int) (paneRenderSnapshotState, bool) {
	changedRows := emu.DrainScreenChangeRows()
	screen, ok := extendScreenSnapshot(emu, prev, width, height, changedRows)
	screenChanged := len(changedRows) != 0 || !ok
	if !ok {
		screen = rebuildScreenSnapshot(emu, width, height)
	}
	return paneRenderSnapshotState{
		screenWidth:  width,
		screenHeight: height,
		screen:       screen,
	}, screenChanged
}

func extendScreenSnapshot(emu mux.TerminalEmulator, prev paneRenderSnapshotState, width, height int, changedRows []int) ([]paneBufferLine, bool) {
	if prev.screen == nil || prev.screenWidth != width || prev.screenHeight != height || len(prev.screen) != height {
		return nil, false
	}
	if len(changedRows) == 0 {
		return prev.screen, true
	}

	screen := append([]paneBufferLine(nil), prev.screen...)
	for _, row := range changedRows {
		if row < 0 || row >= height {
			return nil, false
		}
		screen[row] = captureScreenLine(emu, row, width)
	}
	return screen, true
}

func rebuildScreenSnapshot(emu mux.TerminalEmulator, width, height int) []paneBufferLine {
	screen := make([]paneBufferLine, height)
	for row := range screen {
		screen[row] = captureScreenLine(emu, row, width)
	}
	return screen
}

func captureScreenLine(emu mux.TerminalEmulator, row, width int) paneBufferLine {
	return paneBufferLine{
		text:  emu.ScreenLineText(row),
		cells: captureScreenCells(emu, row, width),
	}
}

func mergePaneSnapshotState(scrollbackState, screenState paneRenderSnapshotState) paneRenderSnapshotState {
	return paneRenderSnapshotState{
		scrollbackLen:    scrollbackState.scrollbackLen,
		scrollbackPushed: scrollbackState.scrollbackPushed,
		scrollback:       scrollbackState.scrollback,
		screenWidth:      screenState.screenWidth,
		screenHeight:     screenState.screenHeight,
		screen:           screenState.screen,
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
