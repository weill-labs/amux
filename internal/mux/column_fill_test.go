package mux

import "testing"

func TestPlanColumnFillSpawnSplitsBottomPaneOfShortestUnderfilledColumn(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)
	p5 := fakePaneID(5)

	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, p2, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p2.ID, SplitHorizontal, p3, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot pane-4: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p4.ID, SplitVertical, p5, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-4 vertically: %v", err)
	}

	plan, err := w.PlanColumnFillSpawn()
	if err != nil {
		t.Fatalf("PlanColumnFillSpawn: %v", err)
	}
	if plan.RootSplit {
		t.Fatal("RootSplit = true, want false")
	}
	if plan.InheritPaneID != p4.ID {
		t.Fatalf("InheritPaneID = %d, want %d", plan.InheritPaneID, p4.ID)
	}
	if plan.SplitTargetPaneID != p4.ID {
		t.Fatalf("SplitTargetPaneID = %d, want %d", plan.SplitTargetPaneID, p4.ID)
	}
}

func TestPlanColumnFillSpawnPrefersLeftmostColumnOnHeightTie(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)

	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p2.ID, SplitVertical, p3, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 vertically: %v", err)
	}

	plan, err := w.PlanColumnFillSpawn()
	if err != nil {
		t.Fatalf("PlanColumnFillSpawn: %v", err)
	}
	if plan.RootSplit {
		t.Fatal("RootSplit = true, want false")
	}
	if plan.InheritPaneID != p1.ID {
		t.Fatalf("InheritPaneID = %d, want %d", plan.InheritPaneID, p1.ID)
	}
	if plan.SplitTargetPaneID != p1.ID {
		t.Fatalf("SplitTargetPaneID = %d, want %d", plan.SplitTargetPaneID, p1.ID)
	}
}

func TestPlanColumnFillSpawnRootSplitsWhenAllColumnsFull(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)

	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, p2, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-1 horizontally: %v", err)
	}

	plan, err := w.PlanColumnFillSpawn()
	if err != nil {
		t.Fatalf("PlanColumnFillSpawn: %v", err)
	}
	if !plan.RootSplit {
		t.Fatal("RootSplit = false, want true")
	}
	if plan.InheritPaneID != p2.ID {
		t.Fatalf("InheritPaneID = %d, want %d", plan.InheritPaneID, p2.ID)
	}
	if plan.SplitTargetPaneID != 0 {
		t.Fatalf("SplitTargetPaneID = %d, want 0 for root split", plan.SplitTargetPaneID)
	}
}

func TestPlanColumnFillSpawnUsesLogicalRootWhenLeadAnchored(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	p3 := fakePaneID(3)
	p4 := fakePaneID(4)

	w := NewWindow(p1, 120, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot pane-2: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead pane-1: %v", err)
	}
	if _, err := w.SplitPaneWithOptions(p2.ID, SplitHorizontal, p3, SplitOptions{}); err != nil {
		t.Fatalf("Split pane-2 horizontally: %v", err)
	}
	if _, err := w.SplitRoot(SplitVertical, p4); err != nil {
		t.Fatalf("SplitRoot pane-4 in logical root: %v", err)
	}

	plan, err := w.PlanColumnFillSpawn()
	if err != nil {
		t.Fatalf("PlanColumnFillSpawn: %v", err)
	}
	if plan.RootSplit {
		t.Fatal("RootSplit = true, want false")
	}
	if plan.InheritPaneID != p4.ID {
		t.Fatalf("InheritPaneID = %d, want %d", plan.InheritPaneID, p4.ID)
	}
	if plan.SplitTargetPaneID != p4.ID {
		t.Fatalf("SplitTargetPaneID = %d, want %d", plan.SplitTargetPaneID, p4.ID)
	}
}
