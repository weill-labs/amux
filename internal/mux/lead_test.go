package mux

import (
	"reflect"
	"testing"
)

func TestSetLeadPinsPaneToAbsoluteRootLeft(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}

	if err := w.SetLead(p2.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if w.LeadPaneID != p2.ID {
		t.Fatalf("LeadPaneID = %d, want %d", w.LeadPaneID, p2.ID)
	}
	if !w.hasAnchoredLead() {
		t.Fatal("expected anchored lead root shape")
	}
	if got := w.Root.Children[0].Pane; got != p2 {
		t.Fatalf("lead slot pane = %v, want %v", got, p2)
	}
	if got := w.logicalRoot(); got == nil || got.FindPane(p1.ID) == nil {
		t.Fatal("logical root should contain the remaining pane")
	}
}

func TestSetLeadSwitchesAnchoredPaneWithoutNestingOldLeadRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead p1: %v", err)
	}

	if err := w.SetLead(p2.ID); err != nil {
		t.Fatalf("SetLead p2: %v", err)
	}

	if !w.hasAnchoredLead() {
		t.Fatal("expected anchored lead root shape after switching lead")
	}
	if got := w.Root.Children[0].Pane; got != p2 {
		t.Fatalf("lead slot pane = %v, want %v", got, p2)
	}
	if w.logicalRoot().FindPane(p1.ID) == nil {
		t.Fatal("previous lead should remain in the logical root")
	}
	if got := w.PaneCount(); got != 3 {
		t.Fatalf("PaneCount = %d, want 3", got)
	}
}

func TestSetLeadSinglePaneCreatesPendingLead(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if w.LeadPaneID != p1.ID {
		t.Fatalf("LeadPaneID = %d, want %d", w.LeadPaneID, p1.ID)
	}
	if !w.hasPendingLead() {
		t.Fatal("expected single-pane lead to remain pending")
	}
	if w.hasAnchoredLead() {
		t.Fatal("single-pane lead should not materialize an anchored root")
	}
	if !w.Root.IsLeaf() {
		t.Fatal("single-pane lead should leave the root as a leaf")
	}
	if got := w.Root.Pane; got != p1 {
		t.Fatalf("root pane = %v, want %v", got, p1)
	}
}

func TestClosePaneCollapsingLeadWindowRetainsPendingLead(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if err := w.ClosePane(p2.ID); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}

	if w.LeadPaneID != p1.ID {
		t.Fatalf("LeadPaneID after collapse = %d, want %d", w.LeadPaneID, p1.ID)
	}
	if !w.hasPendingLead() {
		t.Fatal("collapsed lead window should retain a pending lead")
	}
	if w.hasAnchoredLead() {
		t.Fatal("collapsed lead window should no longer have an anchored root")
	}
	if !w.Root.IsLeaf() || w.Root.Pane != p1 {
		t.Fatalf("collapsed root = %+v, want leaf for pane-1", w.Root)
	}

	if _, err := w.SplitRoot(SplitHorizontal, p3); err != nil {
		t.Fatalf("SplitRoot p3 after collapse: %v", err)
	}

	if !w.hasAnchoredLead() {
		t.Fatal("first growth after collapse should rematerialize anchored lead layout")
	}
	if w.Root.Dir != SplitVertical {
		t.Fatalf("root dir after rematerializing lead = %v, want %v", w.Root.Dir, SplitVertical)
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane after rematerializing = %v, want %v", got, p1)
	}
	if logical := w.logicalRoot(); logical == nil || logical.FindPane(p3.ID) == nil {
		t.Fatal("logical root should contain the new non-lead pane")
	}
}

func TestUnsetLeadClearsStateButLeavesLayoutIntact(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if err := w.UnsetLead(); err != nil {
		t.Fatalf("UnsetLead: %v", err)
	}

	if w.LeadPaneID != 0 {
		t.Fatalf("LeadPaneID = %d, want 0", w.LeadPaneID)
	}
	if got := w.Root.FindPane(p1.ID); got == nil {
		t.Fatal("layout should still contain the former lead pane")
	}
}

func TestSplitLeadPaneErrorsInBothDirections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  SplitDir
	}{
		{name: "vertical", dir: SplitVertical},
		{name: "horizontal", dir: SplitHorizontal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p1 := fakePaneID(1)
			p2 := fakePaneID(2)
			w := NewWindow(p1, 80, 24)
			if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
				t.Fatalf("SplitRoot: %v", err)
			}
			if err := w.SetLead(p1.ID); err != nil {
				t.Fatalf("SetLead: %v", err)
			}

			p3 := fakePaneID(3)
			if _, err := w.SplitPaneWithOptions(p1.ID, tt.dir, p3, SplitOptions{}); err == nil {
				t.Fatalf("SplitPaneWithOptions(%v) on lead should error", tt.dir)
			}
		})
	}
}

func TestSplitLeadPaneWithWindowRefOptionTargetsLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if _, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, p3, SplitOptions{TreatLeadPaneAsWindowRef: true}); err != nil {
		t.Fatalf("SplitPaneWithOptions on lead with window-ref option: %v", err)
	}

	if !w.hasAnchoredLead() {
		t.Fatal("lead layout should remain anchored after spawn-style split")
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
	logical := w.logicalRoot()
	if logical == nil {
		t.Fatal("logical root = nil")
	}
	if logical.FindPane(p2.ID) == nil || logical.FindPane(p3.ID) == nil {
		t.Fatal("logical root should contain both non-lead panes")
	}
	if logical.FindPane(p2.ID).X != logical.FindPane(p3.ID).X {
		t.Fatalf("non-lead panes should stay in the same column, got x=%d and x=%d", logical.FindPane(p2.ID).X, logical.FindPane(p3.ID).X)
	}
	if logical.FindPane(p2.ID).Y == logical.FindPane(p3.ID).Y {
		t.Fatalf("non-lead panes should stack vertically, got y=%d and y=%d", logical.FindPane(p2.ID).Y, logical.FindPane(p3.ID).Y)
	}
}

func TestSplitRootWithLeadVerticalMutatesLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	if !w.hasAnchoredLead() {
		t.Fatal("expected anchored lead root shape")
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
	logical := w.logicalRoot()
	if logical == nil {
		t.Fatal("logical root = nil")
	}
	if logical.Dir != SplitVertical {
		t.Fatalf("logical root dir = %v, want %v", logical.Dir, SplitVertical)
	}
	if logical.FindPane(p2.ID) == nil || logical.FindPane(p3.ID) == nil {
		t.Fatal("logical root should contain both non-lead panes")
	}
}

func TestSplitRootWithLeadHorizontalMutatesLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if _, err := w.SplitRoot(SplitHorizontal, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	if !w.hasAnchoredLead() {
		t.Fatal("expected anchored lead root shape")
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
	logical := w.logicalRoot()
	if logical == nil {
		t.Fatal("logical root = nil")
	}
	if logical.Dir != SplitHorizontal {
		t.Fatalf("logical root dir = %v, want %v", logical.Dir, SplitHorizontal)
	}
	if logical.FindPane(p2.ID) == nil || logical.FindPane(p3.ID) == nil {
		t.Fatal("logical root should contain both non-lead panes")
	}
}

func TestSplitRootVerticalWithAnchoredLeadEqualizesWrappedColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after vertical SplitRoot = %v, want %v", got, want)
	}
}

func TestSplitRootVerticalWithAnchoredLeadAtClampBoundaryEqualizesColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if !w.ResizePane(p1.ID, "left", 36) {
		t.Fatal("ResizePane should shrink the future lead column to the clamp boundary")
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after vertical SplitRoot = %v, want %v", got, want)
	}
	if got[0] < 24 {
		t.Fatalf("lead column width = %d, want at least clamp boundary 24", got[0])
	}
}

func TestSplitRootVerticalWithAnchoredLeadKeepsEqualColumnsInSameDirection(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{29, 29, 29, 30}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after same-direction SplitRoot = %v, want %v", got, want)
	}
}

func TestClosePaneWithAnchoredLeadEqualizesRemainingTwoColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if err := w.ClosePane(p3.ID); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{59, 60}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after close = %v, want %v", got, want)
	}
}

func TestClosePaneWithAnchoredLeadEqualizesRemainingThreeColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if err := w.ClosePane(p4.ID); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after close = %v, want %v", got, want)
	}
}

func TestClosePaneWithAnchoredLeadPreservesManualLeadWidth(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if !w.ResizeBorder(w.Root.Children[0].W, 0, 21) {
		t.Fatal("ResizeBorder should grow the lead column to 60")
	}

	if err := w.ClosePane(p3.ID); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{60, 59}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after manual close = %v, want %v", got, want)
	}
}

func TestSplitRootHorizontalWithAnchoredLeadPreservesWidths(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	widthsBefore := anchoredLeadColumnWidths(t, w)

	if _, err := w.SplitRoot(SplitHorizontal, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	if !reflect.DeepEqual(got, widthsBefore) {
		t.Fatalf("anchored lead column widths after horizontal SplitRoot = %v, want unchanged %v", got, widthsBefore)
	}
}

func TestMovePaneToRootEdgeWithAnchoredLeadEqualizesWrappedColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitHorizontal, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	if err := w.MovePaneToRootEdge(p3.ID, SplitVertical, true); err != nil {
		t.Fatalf("MovePaneToRootEdge: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after MovePaneToRootEdge = %v, want %v", got, want)
	}
}

func TestMovePaneToRootEdgeWithAnchoredLeadComposesCloseAndSplitEqualize(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if err := w.MovePaneToRootEdge(p2.ID, SplitVertical, true); err != nil {
		t.Fatalf("MovePaneToRootEdge: %v", err)
	}

	got := anchoredLeadColumnWidths(t, w)
	want := []int{39, 39, 40}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anchored lead column widths after MovePaneToRootEdge = %v, want %v", got, want)
	}
	if p2Cell, p3Cell := w.Root.FindPane(p2.ID), w.Root.FindPane(p3.ID); p2Cell == nil || p3Cell == nil || p2Cell.X >= p3Cell.X {
		t.Fatalf("moved pane should remain before pane-3, got p2=%v p3=%v", p2Cell, p3Cell)
	}
}

func TestLeadResizeRoundTripPreservesUnevenColumns(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 80, 23)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}

	for _, tc := range []struct {
		paneID uint32
		newID  uint32
	}{
		{paneID: p2.ID, newID: 5},
		{paneID: p3.ID, newID: 6},
		{paneID: p4.ID, newID: 7},
	} {
		if _, err := w.SplitPaneWithOptions(tc.paneID, SplitHorizontal, fakePaneID(tc.newID), SplitOptions{}); err != nil {
			t.Fatalf("SplitPaneWithOptions(%d): %v", tc.paneID, err)
		}
	}

	if !w.ResizePane(p2.ID, "right", 5) {
		t.Fatal("ResizePane(p2, right, 5) should succeed")
	}
	if !w.ResizePane(p3.ID, "right", 3) {
		t.Fatal("ResizePane(p3, right, 3) should succeed")
	}

	initial := snapshotLeafGeometry(w.Root)

	w.Resize(120, 39)
	w.Resize(80, 23)

	if diff := diffLeafGeometry(initial, snapshotLeafGeometry(w.Root)); diff != "" {
		t.Fatalf("lead layout drifted after resize round-trip:\n%s", diff)
	}
}

func anchoredLeadColumnWidths(t *testing.T, w *Window) []int {
	t.Helper()

	columns := w.anchoredLeadWidthColumns()
	if len(columns) == 0 {
		t.Fatal("anchoredLeadWidthColumns() returned no columns")
	}
	widths := make([]int, 0, len(columns))
	for _, column := range columns {
		widths = append(widths, column.W)
	}
	return widths
}

func TestWindowResizeWithLeadKeepsRightSubtreeProportional(t *testing.T) {
	t.Parallel()

	lead := NewLeaf(fakePaneID(1), 0, 0, 17, 23)
	col1 := NewLeaf(fakePaneID(2), 0, 0, 30, 23)
	col2 := NewLeaf(fakePaneID(3), 0, 0, 30, 23)
	col3 := NewLeaf(fakePaneID(4), 0, 0, 30, 23)

	right := &LayoutCell{
		X:        18,
		Y:        0,
		W:        92,
		H:        23,
		Dir:      SplitVertical,
		Children: []*LayoutCell{col1, col2, col3},
	}
	for _, child := range right.Children {
		child.Parent = right
	}

	root := &LayoutCell{
		X:        0,
		Y:        0,
		W:        110,
		H:        23,
		Dir:      SplitVertical,
		Children: []*LayoutCell{lead, right},
	}
	lead.Parent = root
	right.Parent = root
	root.FixOffsets()

	w := &Window{
		Root:       root,
		ActivePane: lead.Pane,
		Width:      110,
		Height:     23,
		LeadPaneID: lead.Pane.ID,
	}

	w.Resize(344, 23)

	got := []int{lead.W, col1.W, col2.W, col3.W}
	want := []int{50, 97, 97, 97}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resized visual column widths = %v, want %v", got, want)
	}
}

func TestSwapPanesWithLeadErrorsOnLeadButAllowsNonLead(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}

	if err := w.SwapPanes(p1.ID, p2.ID); err == nil {
		t.Fatal("SwapPanes with lead should error")
	}
	if err := w.SwapPanes(p2.ID, p3.ID); err != nil {
		t.Fatalf("SwapPanes among non-lead panes: %v", err)
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
}

func TestSwapTreeWithLeadUsesLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}

	if err := w.SwapTree(p1.ID, p2.ID); err == nil {
		t.Fatal("SwapTree involving lead should error")
	}
	if err := w.SwapTree(p2.ID, p4.ID); err != nil {
		t.Fatalf("SwapTree among non-lead panes: %v", err)
	}

	logical := w.logicalRoot()
	if got := logical.Children[0].Pane; got != p4 {
		t.Fatalf("logical child 0 pane = %v, want %v", got, p4)
	}
	if got := logical.Children[2].Pane; got != p2 {
		t.Fatalf("logical child 2 pane = %v, want %v", got, p2)
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
}

func TestMovePaneWithLeadUsesLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}

	if err := w.MovePane(p1.ID, p2.ID, false); err == nil {
		t.Fatal("MovePane involving lead should error")
	}
	if err := w.MovePane(p4.ID, p2.ID, true); err != nil {
		t.Fatalf("MovePane among non-lead panes: %v", err)
	}

	logical := w.logicalRoot()
	if got := logical.Children[0].Pane; got != p4 {
		t.Fatalf("logical child 0 pane = %v, want %v", got, p4)
	}
	if got := logical.Children[1].Pane; got != p2 {
		t.Fatalf("logical child 1 pane = %v, want %v", got, p2)
	}
}

func TestRotatePanesWithLeadLeavesLeadAnchored(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot p4: %v", err)
	}

	if err := w.RotatePanes(true); err != nil {
		t.Fatalf("RotatePanes: %v", err)
	}

	logical := w.logicalRoot()
	if got := logical.Children[0].Pane; got != p4 {
		t.Fatalf("logical child 0 pane = %v, want %v", got, p4)
	}
	if got := logical.Children[1].Pane; got != p2 {
		t.Fatalf("logical child 1 pane = %v, want %v", got, p2)
	}
	if got := logical.Children[2].Pane; got != p3 {
		t.Fatalf("logical child 2 pane = %v, want %v", got, p3)
	}
	if got := w.Root.Children[0].Pane; got != p1 {
		t.Fatalf("lead slot pane = %v, want %v", got, p1)
	}
}

func TestSwapPaneForwardWithLeadErrorsWhenLeadIsActive(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	w.ActivePane = p1

	if err := w.SwapPaneForward(); err == nil {
		t.Fatal("SwapPaneForward with active lead should error")
	}
	if err := w.SwapPaneBackward(); err == nil {
		t.Fatal("SwapPaneBackward with active lead should error")
	}
}

func TestSwapPaneForwardWithLeadOperatesWithinLogicalRoot(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot p2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot p3: %v", err)
	}
	w.ActivePane = p2

	if err := w.SwapPaneForward(); err != nil {
		t.Fatalf("SwapPaneForward: %v", err)
	}

	logical := w.logicalRoot()
	if got := logical.Children[0].Pane; got != p3 {
		t.Fatalf("logical child 0 pane = %v, want %v", got, p3)
	}
	if got := logical.Children[1].Pane; got != p2 {
		t.Fatalf("logical child 1 pane = %v, want %v", got, p2)
	}
}
