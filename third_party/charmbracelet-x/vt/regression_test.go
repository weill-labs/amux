package vt

import "testing"

func TestReverseIndexClampsOversizedScrollMarginsAfterShrink(t *testing.T) {
	t.Parallel()

	term := NewEmulator(40, 24)
	term.Resize(40, 13)

	if _, err := term.WriteString("\x1b[1;21r\x1b[H\x1bM"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	pos := term.CursorPosition()
	if pos.X != 0 || pos.Y != 0 {
		t.Fatalf("CursorPosition() = (%d, %d), want (0, 0)", pos.X, pos.Y)
	}
	if got := term.CellAt(0, 0).Content; got != " " {
		t.Fatalf("CellAt(0, 0).Content = %q, want blank top cell after reverse index scroll", got)
	}
}
