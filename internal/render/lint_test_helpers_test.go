package render

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func mustWrite(tb testing.TB, writer interface{ Write([]byte) (int, error) }, data []byte) {
	tb.Helper()
	if _, err := writer.Write(data); err != nil {
		tb.Fatalf("Write() error = %v", err)
	}
}

func mustSplitCell(tb testing.TB, cell *mux.LayoutCell, dir mux.SplitDir, pane *mux.Pane) *mux.LayoutCell {
	tb.Helper()
	next, err := cell.Split(dir, pane)
	if err != nil {
		tb.Fatalf("Split() error = %v", err)
	}
	return next
}
