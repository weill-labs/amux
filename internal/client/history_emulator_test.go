package client

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mux"
)

func TestPaneBufferSnapshotCellAccess(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(20, 1)
	if _, err := emu.Write([]byte("\033[31mred\033[0m\r\n\033[32mnext\033[0m")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	snap := capturePaneBufferSnapshot(emu, []string{"base"}, mux.DefaultScrollbackLines)

	screen := snap.ScreenCellAt(0, 0)
	if screen.Char != "n" {
		t.Fatalf("ScreenCellAt(0, 0).Char = %q, want %q", screen.Char, "n")
	}
	if screen.Style.Fg == nil {
		t.Fatal("ScreenCellAt(0, 0).Style.Fg = nil, want green")
	}
	assertSameColor(t, screen.Style.Fg, ansi.BasicColor(2))

	base := snap.ScrollbackCellAt(0, 0)
	if base.Char != "b" {
		t.Fatalf("ScrollbackCellAt(0, 0).Char = %q, want %q", base.Char, "b")
	}
	if base.Style.Fg != nil {
		t.Fatalf("ScrollbackCellAt(0, 0).Style.Fg = %v, want nil", base.Style.Fg)
	}

	live := snap.ScrollbackCellAt(0, 1)
	if live.Char != "r" {
		t.Fatalf("ScrollbackCellAt(0, 1).Char = %q, want %q", live.Char, "r")
	}
	if live.Style.Fg == nil {
		t.Fatal("ScrollbackCellAt(0, 1).Style.Fg = nil, want red")
	}
	assertSameColor(t, live.Style.Fg, ansi.BasicColor(1))
}

func TestPaneBufferSnapshotScrollbackCellAtOutOfRange(t *testing.T) {
	t.Parallel()

	snap := capturePaneBufferSnapshot(newTestVTEmulator(20, 1), []string{"base"}, mux.DefaultScrollbackLines)

	if got := snap.ScrollbackCellAt(0, -1); got.Char != " " || got.Width != 1 {
		t.Fatalf("ScrollbackCellAt(0, -1) = %+v, want space width 1", got)
	}
	if got := snap.ScrollbackCellAt(99, 0); got.Char != " " || got.Width != 1 {
		t.Fatalf("ScrollbackCellAt(99, 0) = %+v, want space width 1", got)
	}
}

func assertSameColor(t *testing.T, got, want interface{ RGBA() (r, g, b, a uint32) }) {
	t.Helper()
	gotR, gotG, gotB, gotA := got.RGBA()
	wantR, wantG, wantB, wantA := want.RGBA()
	if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
		t.Fatalf("color = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
			gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
	}
}
