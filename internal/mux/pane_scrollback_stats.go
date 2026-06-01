package mux

import (
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
)

const screenBufferCount = 2 // vt keeps main and alternate screen buffers.

// PaneScrollbackStats is a cheap memory-oriented snapshot of server-retained
// pane history. Byte fields are estimates; they avoid cloning styled history.
type PaneScrollbackStats struct {
	PaneID         uint32
	PaneName       string
	Host           string
	Width          int
	Height         int
	LimitLines     int
	BaseLines      int
	LiveLines      int
	TotalLines     int
	EffectiveLines int
	BaseBytes      uint64
	LiveBytes      uint64
	ScreenBytes    uint64
	EstimatedBytes uint64
}

// ScrollbackStats returns cheap line counts and retained-memory estimates for
// this pane's base history, live VT scrollback, and terminal screen buffers.
func (p *Pane) ScrollbackStats() PaneScrollbackStats {
	if p == nil {
		return PaneScrollbackStats{}
	}
	return paneActorValue(p, func() PaneScrollbackStats {
		return p.scrollbackStatsLocked()
	})
}

func (p *Pane) scrollbackStatsLocked() PaneScrollbackStats {
	limit := effectivePaneScrollbackLimit(p)
	baseHistory := p.loadBaseHistory()
	width, height := 0, 0
	if p.emulator != nil {
		width, height = p.emulator.Size()
	}

	liveLines, liveBytes := estimateEmulatorScrollbackBytes(p.emulator, limit)
	baseBytes := estimateStringSliceBytes(baseHistory)
	screenBytes := estimateScreenBufferBytes(width, height)
	totalLines := len(baseHistory) + liveLines

	return PaneScrollbackStats{
		PaneID:         p.ID,
		PaneName:       p.Meta.Name,
		Host:           p.Meta.Host,
		Width:          width,
		Height:         height,
		LimitLines:     limit,
		BaseLines:      len(baseHistory),
		LiveLines:      liveLines,
		TotalLines:     totalLines,
		EffectiveLines: min(totalLines, limit),
		BaseBytes:      baseBytes,
		LiveBytes:      liveBytes,
		ScreenBytes:    screenBytes,
		EstimatedBytes: baseBytes + liveBytes + screenBytes,
	}
}

func effectivePaneScrollbackLimit(p *Pane) int {
	if p == nil {
		return DefaultScrollbackLines
	}
	if p.scrollbackLimit > 0 {
		return p.scrollbackLimit
	}
	return effectiveScrollbackLines(p.scrollbackLines)
}

func estimateStringSliceBytes(lines []string) uint64 {
	if len(lines) == 0 && cap(lines) == 0 {
		return 0
	}
	total := uint64(cap(lines)) * stringHeaderBytes()
	for _, line := range lines {
		total += uint64(len(line))
	}
	return total
}

func estimateEmulatorScrollbackBytes(emu TerminalEmulator, limit int) (lines int, bytes uint64) {
	if emu == nil {
		return 0, 0
	}
	lines = emu.ScrollbackLen()
	if vte, ok := emu.(*vtEmulator); ok {
		return lines, vte.estimateScrollbackBytes()
	}
	width, _ := emu.Size()
	if width < 0 {
		width = 0
	}
	if limit < 0 {
		limit = 0
	}
	bytes = uint64(limit)*lineHeaderBytes() + uint64(lines)*uint64(width)*cellBytes()
	return lines, bytes
}

func (v *vtEmulator) estimateScrollbackBytes() uint64 {
	if v == nil || v.emu == nil {
		return 0
	}
	limit := v.scrollbackLimit
	if limit < 0 {
		limit = 0
	}
	total := uint64(limit) * lineHeaderBytes()
	sb := v.emu.Scrollback()
	if sb == nil {
		return total
	}
	for row := 0; row < sb.Len(); row++ {
		line := sb.Line(row)
		total += uint64(cap(line)) * cellBytes()
		for _, cell := range line {
			total += uint64(len(cell.Content))
		}
	}
	return total
}

func estimateScreenBufferBytes(width, height int) uint64 {
	if width <= 0 || height <= 0 {
		return 0
	}
	perLine := uint64(width)*cellBytes() + lineHeaderBytes()
	perBuffer := uint64(height)*perLine + uint64(height)*pointerBytes()
	return screenBufferCount * perBuffer
}

func cellBytes() uint64 {
	return uint64(unsafe.Sizeof(uv.Cell{}))
}

func lineHeaderBytes() uint64 {
	return uint64(unsafe.Sizeof(uv.Line(nil)))
}

func stringHeaderBytes() uint64 {
	return uint64(unsafe.Sizeof(""))
}

func pointerBytes() uint64 {
	return uint64(unsafe.Sizeof((*byte)(nil)))
}
