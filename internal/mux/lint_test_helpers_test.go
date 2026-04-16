package mux

import "testing"

func mustWrite(tb testing.TB, writer interface{ Write([]byte) (int, error) }, data []byte) {
	tb.Helper()
	if _, err := writer.Write(data); err != nil {
		tb.Fatalf("Write() error = %v", err)
	}
}

func mustSplitCell(tb testing.TB, cell *LayoutCell, dir SplitDir, pane *Pane) *LayoutCell {
	tb.Helper()
	next, err := cell.Split(dir, pane)
	if err != nil {
		tb.Fatalf("Split() error = %v", err)
	}
	return next
}

func mustRotate(tb testing.TB, w *Window, forward bool) {
	tb.Helper()
	if err := w.RotatePanes(forward); err != nil {
		tb.Fatalf("RotatePanes() error = %v", err)
	}
}
