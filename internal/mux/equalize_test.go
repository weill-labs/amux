package mux

import (
	"reflect"
	"testing"
)

func TestWindowEqualizeRootColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}

	if !w.ResizePane(p1.ID, "right", 6) {
		t.Fatal("ResizePane should skew root column widths")
	}

	if !w.Equalize(true, false) {
		t.Fatal("Equalize(widths=true, heights=false) = false, want true")
	}

	got := []int{w.Root.Children[0].W, w.Root.Children[1].W, w.Root.Children[2].W}
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("root column widths after equalize = %v, want %v", got, want)
	}
}

func TestWindowEqualizeVerticalWithinColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	p5 := fakePaneID(5)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}

	w.FocusPane(p2)
	if _, err := w.SplitWithOptions(SplitHorizontal, p4, SplitOptions{KeepFocus: true}); err != nil {
		t.Fatalf("SplitWithOptions pane-4: %v", err)
	}
	if _, err := w.SplitWithOptions(SplitHorizontal, p5, SplitOptions{KeepFocus: true}); err != nil {
		t.Fatalf("SplitWithOptions pane-5: %v", err)
	}

	if !w.ResizePane(p2.ID, "down", 3) {
		t.Fatal("ResizePane should skew middle-column row heights")
	}

	middle := w.Root.Children[1]
	if middle.IsLeaf() || middle.Dir != SplitHorizontal {
		t.Fatalf("middle root child = %+v, want horizontal stack", middle)
	}

	widthsBefore := []int{w.Root.Children[0].W, w.Root.Children[1].W, w.Root.Children[2].W}
	if !w.Equalize(false, true) {
		t.Fatal("Equalize(widths=false, heights=true) = false, want true")
	}

	gotHeights := []int{middle.Children[0].H, middle.Children[1].H, middle.Children[2].H}
	wantHeights := []int{7, 7, 8}
	if !reflect.DeepEqual(gotHeights, wantHeights) {
		t.Fatalf("middle column heights after vertical equalize = %v, want %v", gotHeights, wantHeights)
	}

	gotWidths := []int{w.Root.Children[0].W, w.Root.Children[1].W, w.Root.Children[2].W}
	if !reflect.DeepEqual(gotWidths, widthsBefore) {
		t.Fatalf("vertical equalize changed root column widths = %v, want %v", gotWidths, widthsBefore)
	}
}

func TestWindowEqualizeRebalancesNestedColumnsUnderHorizontalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 246, 30)
	for id := uint32(2); id <= 6; id++ {
		if _, err := w.SplitRoot(SplitVertical, fakePaneID(id)); err != nil {
			t.Fatalf("SplitRoot pane-%d: %v", id, err)
		}
	}
	if _, err := w.SplitRoot(SplitHorizontal, fakePaneID(147)); err != nil {
		t.Fatalf("SplitRoot pane-147: %v", err)
	}

	if !w.ResizePane(p1.ID, "right", 46) {
		t.Fatal("ResizePane should skew top-row column widths")
	}

	top := w.Root.Children[0]
	if top.IsLeaf() || top.Dir != SplitVertical {
		t.Fatalf("top row = %+v, want vertical columns", top)
	}

	widthsBefore := make([]int, 0, len(top.Children))
	for _, child := range top.Children {
		widthsBefore = append(widthsBefore, child.W)
	}
	if reflect.DeepEqual(widthsBefore, []int{40, 40, 40, 40, 40, 41}) {
		t.Fatalf("top-row widths before equalize = %v, want skewed widths", widthsBefore)
	}

	if !w.Equalize(true, true) {
		t.Fatal("Equalize(widths=true, heights=true) = false, want true")
	}

	got := make([]int, 0, len(top.Children))
	for _, child := range top.Children {
		got = append(got, child.W)
	}
	want := []int{40, 40, 40, 40, 40, 41}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top-row column widths after equalize = %v, want %v", got, want)
	}
}

func TestWindowEqualizeUsesLogicalRootWhenLeadAnchored(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	w.FocusPane(p2)
	if _, err := w.SplitWithOptions(SplitHorizontal, p4, SplitOptions{KeepFocus: true}); err != nil {
		t.Fatalf("SplitWithOptions pane-4: %v", err)
	}
	if !w.ResizePane(p2.ID, "right", 4) {
		t.Fatal("ResizePane should skew logical-root column widths")
	}
	if !w.ResizePane(p2.ID, "down", 2) {
		t.Fatal("ResizePane should skew logical-root row heights")
	}

	leadWidth := w.Root.Children[0].W
	if !w.Equalize(true, true) {
		t.Fatal("Equalize(widths=true, heights=true) = false, want true")
	}

	if got := w.Root.Children[0].W; got != leadWidth {
		t.Fatalf("lead width after equalize = %d, want %d", got, leadWidth)
	}

	logical := w.logicalRoot()
	gotWidths := []int{logical.Children[0].W, logical.Children[1].W}
	wantWidths := []int{26, 26}
	if !reflect.DeepEqual(gotWidths, wantWidths) {
		t.Fatalf("logical-root column widths after equalize = %v, want %v", gotWidths, wantWidths)
	}

	leftColumn := logical.Children[0]
	gotHeights := []int{leftColumn.Children[0].H, leftColumn.Children[1].H}
	wantHeights := []int{11, 12}
	if !reflect.DeepEqual(gotHeights, wantHeights) {
		t.Fatalf("logical-root row heights after equalize = %v, want %v", gotHeights, wantHeights)
	}
}

func TestWindowEqualizeNoopKeepsZoom(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot pane-3: %v", err)
	}
	if err := w.Zoom(p2.ID); err != nil {
		t.Fatalf("Zoom pane-2: %v", err)
	}

	widthsBefore := []int{w.Root.Children[0].W, w.Root.Children[1].W, w.Root.Children[2].W}

	if w.Equalize(true, false) {
		t.Fatal("Equalize(widths=true, heights=false) = true, want false for an already balanced layout")
	}
	if got := w.ZoomedPaneID; got != p2.ID {
		t.Fatalf("ZoomedPaneID after no-op equalize = %d, want %d", got, p2.ID)
	}

	widthsAfter := []int{w.Root.Children[0].W, w.Root.Children[1].W, w.Root.Children[2].W}
	if !reflect.DeepEqual(widthsAfter, widthsBefore) {
		t.Fatalf("balanced widths changed after no-op equalize = %v, want %v", widthsAfter, widthsBefore)
	}
}
