package mux

import "testing"

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

func TestSetLeadSinglePaneErrors(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	if err := w.SetLead(p1.ID); err == nil {
		t.Fatal("SetLead on single pane should error")
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
