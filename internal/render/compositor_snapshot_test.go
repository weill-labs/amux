package render

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestRenderDiffPublishesPrevGridSnapshot(t *testing.T) {
	t.Parallel()

	comp := NewCompositor(20, 6, "test")
	root := mux.NewLeaf(&mux.Pane{ID: 1}, 0, 0, 20, 5)
	lookup := func(uint32) PaneData {
		return &statusPaneData{
			id:     1,
			name:   "pane-1",
			color:  config.TextColorHex,
			screen: "snapshot row\n\n\n",
		}
	}

	comp.RenderDiffWithOverlayDirtyStats(root, 1, lookup, OverlayState{}, nil, true)

	snap := comp.prevGridSnap.Load()
	if snap == nil {
		t.Fatal("RenderDiff should publish a prevGrid snapshot")
	}
	if snap == comp.prevGrid {
		t.Fatal("published snapshot should be a clone, not the mutable prevGrid pointer")
	}
	if got, want := gridToText(snap), gridToText(comp.prevGrid); got != want {
		t.Fatalf("published snapshot text mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	comp.prevGrid.Set(0, 1, ScreenCell{Char: "X", Width: 1})
	if got := gridToText(snap); strings.Contains(got, "Xnapshot row") {
		t.Fatalf("published snapshot changed after prevGrid mutation:\n%s", got)
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
