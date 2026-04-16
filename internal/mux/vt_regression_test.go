package mux

import "testing"

func TestVTEmulatorClampsOversizedScrollMarginsAfterShrink(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulator(40, 24)
	emu.Resize(40, 13)

	// Regression: after a shrink, some apps still emit scroll margins for the
	// old height. Reverse index at the top margin used to panic inside x/vt.
	mustWrite(t, emu, []byte("\x1b[1;21r\x1b[H\x1bM"))

	col, row := emu.CursorPosition()
	if col != 0 || row != 0 {
		t.Fatalf("CursorPosition() = (%d, %d), want (0, 0)", col, row)
	}
	if got := emu.ScreenLineText(0); got != "" {
		t.Fatalf("ScreenLineText(0) = %q, want blank top line after reverse index scroll", got)
	}
}
