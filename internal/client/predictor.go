package client

import (
	"time"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/mattn/go-runewidth"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

type localEchoMode string

const (
	localEchoModeOff    localEchoMode = "off"
	localEchoModeAuto   localEchoMode = "auto"
	localEchoModeAlways localEchoMode = "always"
)

type localEchoStyle string

const (
	localEchoStyleDim       localEchoStyle = "dim"
	localEchoStyleUnderline localEchoStyle = "underline"
	localEchoStyleNone      localEchoStyle = "none"
)

const (
	localEchoAckWindow           = 3
	localEchoPasteBurstThreshold = 3
	localEchoPasteWindow         = 20 * time.Millisecond
)

type panePredictionContext struct {
	Width       int
	Height      int
	CursorCol   int
	CursorRow   int
	AltScreen   bool
	CursorStyle string
}

type panePredictionBase struct {
	Width  int
	Height int
	Screen string
}

type panePredictionSnapshot struct {
	Width        int
	Height       int
	CursorCol    int
	CursorRow    int
	CursorHidden bool
	Screen       string
	Cells        []render.ScreenCell
}

func (s panePredictionSnapshot) cellAt(col, row int) render.ScreenCell {
	if col < 0 || row < 0 || col >= s.Width || row >= s.Height {
		return render.ScreenCell{Char: " ", Width: 1}
	}
	return s.Cells[row*s.Width+col]
}

func (s panePredictionSnapshot) equal(other panePredictionSnapshot) bool {
	if s.Width != other.Width ||
		s.Height != other.Height ||
		s.CursorCol != other.CursorCol ||
		s.CursorRow != other.CursorRow ||
		s.CursorHidden != other.CursorHidden ||
		len(s.Cells) != len(other.Cells) {
		return false
	}
	for i := range s.Cells {
		if !s.Cells[i].Equal(other.Cells[i]) {
			return false
		}
	}
	return true
}

type pendingPrediction struct {
	epoch    uint32
	data     []byte
	snapshot panePredictionSnapshot
}

type panePredictorState struct {
	shadow        mux.TerminalEmulator
	pending       []pendingPrediction
	recentAcks    []bool
	recentPresses []time.Time
}

type predictorReconcileResult struct {
	Matched bool
	Pending int
	Changed bool
}

type predictor struct {
	mode      localEchoMode
	style     localEchoStyle
	nextEpoch uint32
	panes     map[uint32]*panePredictorState
}

func newPredictor(mode localEchoMode, style localEchoStyle) *predictor {
	return &predictor{
		mode:  mode,
		style: style,
		panes: make(map[uint32]*panePredictorState),
	}
}

func parseLocalEchoMode(mode string) localEchoMode {
	switch mode {
	case "always":
		return localEchoModeAlways
	case "off":
		return localEchoModeOff
	default:
		return localEchoModeAuto
	}
}

func parseLocalEchoStyle(style string) localEchoStyle {
	switch style {
	case "underline":
		return localEchoStyleUnderline
	case "none":
		return localEchoStyleNone
	default:
		return localEchoStyleDim
	}
}

func (p *predictor) configure(mode, style string) {
	if p == nil {
		return
	}
	p.mode = parseLocalEchoMode(mode)
	p.style = parseLocalEchoStyle(style)
	if p.mode == localEchoModeOff {
		p.clear()
	}
}

func (p *predictor) enabled() bool {
	return p != nil && p.mode != localEchoModeOff
}

func (p *predictor) stateForPane(paneID uint32) *panePredictorState {
	if paneID == 0 {
		return nil
	}
	if state := p.panes[paneID]; state != nil {
		return state
	}
	state := &panePredictorState{}
	p.panes[paneID] = state
	return state
}

func (p *predictor) clearPane(paneID uint32) {
	if p == nil {
		return
	}
	state := p.panes[paneID]
	if state == nil {
		return
	}
	if state.shadow != nil {
		_ = state.shadow.Close()
	}
	delete(p.panes, paneID)
}

func (p *predictor) clear() {
	if p == nil {
		return
	}
	for paneID := range p.panes {
		p.clearPane(paneID)
	}
}

func (p *predictor) prune(valid map[uint32]bool, clearAll bool) {
	if p == nil {
		return
	}
	if clearAll {
		p.clear()
		return
	}
	for paneID := range p.panes {
		if valid[paneID] {
			continue
		}
		p.clearPane(paneID)
	}
}

func (p *predictor) nextPredictionEpoch() uint32 {
	p.nextEpoch++
	if p.nextEpoch == 0 {
		p.nextEpoch++
	}
	return p.nextEpoch
}

func trimPressHistory(presses []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-localEchoPasteWindow)
	trimmed := presses[:0]
	for _, ts := range presses {
		if ts.Before(cutoff) {
			continue
		}
		trimmed = append(trimmed, ts)
	}
	return trimmed
}

func singlePrintableInput(data []byte) (string, int, bool) {
	if len(data) == 0 {
		return "", 0, false
	}
	r, size := utf8.DecodeRune(data)
	if r == utf8.RuneError && size == 1 {
		return "", 0, false
	}
	if size != len(data) || unicode.IsControl(r) {
		return "", 0, false
	}
	width := runewidth.RuneWidth(r)
	if width < 1 {
		width = 1
	}
	return string(r), width, true
}

func predictionUsesInsertCursor(style string) bool {
	return style == "bar" || style == "underline"
}

func hasRecentConfirmingAcks(state *panePredictorState) bool {
	if state == nil || len(state.recentAcks) < localEchoAckWindow {
		return false
	}
	for _, confirming := range state.recentAcks[len(state.recentAcks)-localEchoAckWindow:] {
		if !confirming {
			return false
		}
	}
	return true
}

func (p *predictor) allowPrediction(paneID uint32, ctx panePredictionContext, data []byte, now time.Time) bool {
	if !p.enabled() {
		return false
	}
	if _, _, ok := singlePrintableInput(data); !ok {
		return false
	}

	state := p.stateForPane(paneID)
	state.recentPresses = append(trimPressHistory(state.recentPresses, now), now)

	if p.mode == localEchoModeAlways {
		return true
	}

	if len(state.recentPresses) > localEchoPasteBurstThreshold {
		return false
	}
	if ctx.Height > 1 && ctx.CursorRow >= ctx.Height-2 {
		return false
	}
	if ctx.AltScreen && !predictionUsesInsertCursor(ctx.CursorStyle) {
		return false
	}
	return hasRecentConfirmingAcks(state)
}

func buildPredictionShadow(base panePredictionBase) mux.TerminalEmulator {
	if base.Width <= 0 || base.Height <= 0 {
		return nil
	}
	shadow := mux.NewVTEmulatorWithDrainAndScrollback(base.Width, base.Height, mux.DefaultScrollbackLines)
	_, _ = shadow.Write([]byte(base.Screen))
	return shadow
}

func (p *predictor) ensureShadow(state *panePredictorState, base panePredictionBase) mux.TerminalEmulator {
	if state == nil {
		return nil
	}
	if state.shadow != nil {
		width, height := state.shadow.Size()
		if width == base.Width && height == base.Height {
			return state.shadow
		}
		_ = state.shadow.Close()
		state.shadow = nil
	}
	state.shadow = buildPredictionShadow(base)
	return state.shadow
}

func (p *predictor) predict(paneID uint32, base panePredictionBase, data []byte, _ time.Time) (uint32, bool) {
	if !p.enabled() {
		return 0, false
	}
	if _, _, ok := singlePrintableInput(data); !ok {
		return 0, false
	}
	epoch := p.nextPredictionEpoch()
	return epoch, p.predictWithEpoch(paneID, base, data, epoch)
}

func (p *predictor) prepareInput(paneID uint32, ctx panePredictionContext, base panePredictionBase, data []byte, now time.Time) (uint32, bool) {
	if !p.enabled() {
		return 0, false
	}
	if _, _, ok := singlePrintableInput(data); !ok {
		return 0, false
	}
	predicted := p.allowPrediction(paneID, ctx, data, now)
	epoch := p.nextPredictionEpoch()
	if !predicted {
		return epoch, false
	}
	return epoch, p.predictWithEpoch(paneID, base, data, epoch)
}

func (p *predictor) predictWithEpoch(paneID uint32, base panePredictionBase, data []byte, epoch uint32) bool {
	state := p.stateForPane(paneID)
	shadow := p.ensureShadow(state, base)
	if shadow == nil {
		return false
	}
	_, _ = shadow.Write(data)
	state.pending = append(state.pending, pendingPrediction{
		epoch:    epoch,
		data:     append([]byte(nil), data...),
		snapshot: capturePanePredictionSnapshot(shadow),
	})
	return true
}

func (p *predictor) shadow(paneID uint32) (mux.TerminalEmulator, bool) {
	if p == nil {
		return nil, false
	}
	state := p.panes[paneID]
	if state == nil || state.shadow == nil {
		return nil, false
	}
	return state.shadow, true
}

func (p *predictor) hasShadow(paneID uint32) bool {
	_, ok := p.shadow(paneID)
	return ok
}

func (p *predictor) recordAck(paneID uint32, confirming bool) {
	if p == nil || paneID == 0 {
		return
	}
	state := p.stateForPane(paneID)
	state.recentAcks = append(state.recentAcks, confirming)
	if len(state.recentAcks) > localEchoAckWindow {
		state.recentAcks = append([]bool(nil), state.recentAcks[len(state.recentAcks)-localEchoAckWindow:]...)
	}
}

func (p *predictor) reconcile(paneID uint32, confirmed panePredictionSnapshot, sourceEpoch uint32) predictorReconcileResult {
	if p == nil || paneID == 0 {
		return predictorReconcileResult{}
	}
	state := p.panes[paneID]
	if state == nil || sourceEpoch == 0 {
		if state == nil {
			return predictorReconcileResult{}
		}
		return predictorReconcileResult{Pending: len(state.pending)}
	}

	index := -1
	for i, pending := range state.pending {
		if pending.epoch > sourceEpoch {
			break
		}
		index = i
	}
	if index < 0 {
		return predictorReconcileResult{Pending: len(state.pending)}
	}

	matched := state.pending[index].snapshot.equal(confirmed)
	remaining := append([]pendingPrediction(nil), state.pending[index+1:]...)
	state.pending = remaining

	if matched {
		if len(state.pending) == 0 {
			p.clearPane(paneID)
		}
		return predictorReconcileResult{Matched: true, Pending: len(remaining), Changed: true}
	}

	if len(remaining) == 0 {
		p.clearPane(paneID)
		return predictorReconcileResult{Matched: false, Pending: 0, Changed: true}
	}

	if state.shadow != nil {
		_ = state.shadow.Close()
	}
	state.shadow = buildPredictionShadow(panePredictionBase{
		Width:  confirmed.Width,
		Height: confirmed.Height,
		Screen: confirmed.Screen,
	})
	if state.shadow == nil {
		delete(p.panes, paneID)
		return predictorReconcileResult{Matched: false, Pending: 0, Changed: true}
	}
	for i := range state.pending {
		_, _ = state.shadow.Write(state.pending[i].data)
		state.pending[i].snapshot = capturePanePredictionSnapshot(state.shadow)
	}
	return predictorReconcileResult{Matched: false, Pending: len(state.pending), Changed: true}
}

func classifyPredictionAck(before, after panePredictionSnapshot, data []byte) bool {
	char, width, ok := singlePrintableInput(data)
	if !ok {
		return false
	}
	if before.CursorHidden || after.CursorHidden {
		return false
	}
	if before.CursorRow != after.CursorRow {
		return false
	}
	if after.CursorCol != before.CursorCol+width {
		return false
	}
	cell := after.cellAt(before.CursorCol, before.CursorRow)
	return cell.Char == char
}

func predictionCellChanged(confirmed, predicted render.ScreenCell) bool {
	return !predicted.Equal(confirmed)
}

func applyPredictionStyle(cell render.ScreenCell, style localEchoStyle) render.ScreenCell {
	switch style {
	case localEchoStyleUnderline:
		if cell.Style.Underline == uv.UnderlineStyleNone {
			cell.Style.Underline = uv.UnderlineStyleSingle
		}
	case localEchoStyleNone:
		return cell
	default:
		cell.Style.Attrs |= uv.AttrFaint
	}
	return cell
}
