package client

import (
	"github.com/weill-labs/amux/internal/mux"
)

// historyEmulator presents retained server history plus post-attach emulator
// scrollback as one scrollback source for client-local copy mode.
type historyEmulator struct {
	emu             mux.TerminalEmulator
	baseHistory     []string
	scrollbackLines int
}

func (h *historyEmulator) Size() (width, height int) {
	return h.emu.Size()
}

func (h *historyEmulator) ScreenLineText(y int) string {
	return h.emu.ScreenLineText(y)
}

func (h *historyEmulator) ScrollbackLen() int {
	baseStart, liveStart := h.starts()
	return len(h.baseHistory) - baseStart + h.emu.ScrollbackLen() - liveStart
}

func (h *historyEmulator) ScrollbackLineText(y int) string {
	baseStart, liveStart := h.starts()
	baseLen := len(h.baseHistory) - baseStart
	if y < 0 {
		return ""
	}
	if y < baseLen {
		return h.baseHistory[baseStart+y]
	}
	return h.emu.ScrollbackLineText(liveStart + y - baseLen)
}

func (h *historyEmulator) starts() (baseStart, liveStart int) {
	liveLen := h.emu.ScrollbackLen()
	total := len(h.baseHistory) + liveLen
	limit := h.scrollbackLines
	if limit <= 0 {
		limit = mux.DefaultScrollbackLines
	}
	if total <= limit {
		return 0, 0
	}

	drop := total - limit
	if drop >= len(h.baseHistory) {
		return len(h.baseHistory), drop - len(h.baseHistory)
	}
	return drop, 0
}
