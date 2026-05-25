package render

import (
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestRenderDiffPublishesOwnedPrevGridSnapshot(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(20, 6, "test")
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 5)
	screen := "snapshot row\n\n\n"
	lookup := func(uint32) PaneData {
		return &statusPaneData{
			id:     1,
			name:   "pane-1",
			color:  config.TextColorHex,
			screen: screen,
		}
	}

	comp.RenderDiffWithOverlayDirtyStats(root, 1, lookup, OverlayState{}, nil, true)

	snap := comp.prevGridSnap.Load()
	if snap == nil {
		t.Fatal("RenderDiff should publish a prevGrid snapshot")
	}
	if snap != comp.prevGrid {
		t.Fatal("published snapshot should own the rendered prevGrid pointer without cloning")
	}
	firstSnapshotText := gridToText(snap)
	if got, want := firstSnapshotText, gridToText(comp.prevGrid); got != want {
		t.Fatalf("published snapshot text mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	screen = "updated row\n\n\n"
	comp.RenderDiffWithOverlayDirtyStats(root, 1, lookup, OverlayState{}, map[uint32]struct{}{1: {}}, false)
	if got := gridToText(snap); got != firstSnapshotText {
		t.Fatalf("previous published snapshot changed after next render:\n--- got ---\n%s\n--- want ---\n%s", got, firstSnapshotText)
	}
	if next := comp.prevGridSnap.Load(); next == nil || next == snap {
		t.Fatal("next render should publish the newly rendered grid")
	}
}

func TestPublishPrevGridSnapshotDoesNotAllocate(t *testing.T) {
	comp := NewCompositor(80, 24, "test")
	grid := NewScreenGrid(80, 24)

	allocs := testing.AllocsPerRun(1000, func() {
		comp.publishPrevGridSnapshot(grid)
	})
	if allocs > 0 {
		t.Fatalf("publishPrevGridSnapshot allocated %.2f times; want ownership transfer without allocation", allocs)
	}
}

func TestPrevGridSnapshotClearsWithPrevGridInvalidation(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(20, 6, "test")
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 5)
	lookup := func(uint32) PaneData {
		return &statusPaneData{id: 1, name: "pane-1", color: config.TextColorHex}
	}

	comp.RenderDiff(root, 1, lookup)
	if comp.prevGridSnap.Load() == nil {
		t.Fatal("RenderDiff should publish a snapshot")
	}

	comp.Resize(30, 8)
	if comp.prevGrid != nil {
		t.Fatal("Resize should clear prevGrid")
	}
	if comp.prevGridSnap.Load() != nil {
		t.Fatal("Resize should clear the published prevGrid snapshot")
	}

	comp.RenderDiff(root, 1, lookup)
	if comp.prevGridSnap.Load() == nil {
		t.Fatal("RenderDiff should republish a snapshot after resize")
	}

	comp.ClearPrevGrid()
	if comp.prevGrid != nil {
		t.Fatal("ClearPrevGrid should clear prevGrid")
	}
	if comp.prevGridSnap.Load() != nil {
		t.Fatal("ClearPrevGrid should clear the published prevGrid snapshot")
	}
}

func TestPrevGridTextRectCropsPublishedSnapshot(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(6, 4, "test")
	grid := NewScreenGrid(6, 4)
	for y, row := range []string{"abcdef", "ghijkl", "mnopqr", "stuvwx"} {
		for x, ch := range row {
			grid.Set(x, y, ScreenCell{Char: string(ch), Width: 1})
		}
	}

	comp.publishPrevGridSnapshot(grid)

	if got, want := comp.PrevGridTextRect(1, 1, 3, 2), "hij\nnop"; got != want {
		t.Fatalf("PrevGridTextRect() = %q, want %q", got, want)
	}
	if got, want := comp.PrevGridTextRect(-2, -1, 4, 3), "ab\ngh"; got != want {
		t.Fatalf("clipped PrevGridTextRect() = %q, want %q", got, want)
	}
	if got := comp.PrevGridTextRect(10, 10, 1, 1); got != "" {
		t.Fatalf("out-of-bounds PrevGridTextRect() = %q, want empty", got)
	}
	if got := comp.PrevGridTextRect(0, 0, 0, 1); got != "" {
		t.Fatalf("zero-width PrevGridTextRect() = %q, want empty", got)
	}

	blankAndWide := NewScreenGrid(4, 1)
	blankAndWide.Set(0, 0, ScreenCell{Char: "a", Width: 1})
	blankAndWide.Set(1, 0, ScreenCell{Width: 1})
	blankAndWide.Set(2, 0, ScreenCell{Char: "b", Width: 1})
	blankAndWide.Set(3, 0, ScreenCell{Width: 0})
	if got, want := gridRectToText(blankAndWide, 0, 0, 4, 1), "a b"; got != want {
		t.Fatalf("gridRectToText blank/wide row = %q, want %q", got, want)
	}

	comp.publishPrevGridSnapshot(nil)
	if got := comp.PrevGridTextRect(0, 0, 1, 1); got != "" {
		t.Fatalf("cleared PrevGridTextRect() = %q, want empty", got)
	}
}
