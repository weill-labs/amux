package mux

import (
	"testing"
)

func TestSetLead_PinsToLeft(t *testing.T) {
	t.Parallel()

	// Create a 2-pane window: p1 (left) | p2 (right)
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}

	// Set p2 (currently on the right) as lead -- it should move to the left
	if err := w.SetLead(p2.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	if w.LeadPaneID != p2.ID {
		t.Errorf("LeadPaneID = %d, want %d", w.LeadPaneID, p2.ID)
	}

	// Root must be SplitVertical with exactly 2 children
	if w.Root.IsLeaf() {
		t.Fatal("root should not be a leaf")
	}
	if w.Root.Dir != SplitVertical {
		t.Fatalf("root dir = %d, want SplitVertical(%d)", w.Root.Dir, SplitVertical)
	}
	if len(w.Root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(w.Root.Children))
	}

	// Children[0] must contain the lead pane
	left := w.Root.Children[0]
	if !left.IsLeaf() || left.Pane != p2 {
		t.Errorf("Children[0] should be lead pane p2, got pane %v", left.Pane)
	}

	// Lead pane should be full height
	if left.H != w.Height {
		t.Errorf("lead pane height = %d, want %d", left.H, w.Height)
	}
}

func TestSetLead_SinglePaneErrors(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	err := w.SetLead(p1.ID)
	if err == nil {
		t.Fatal("SetLead on single pane should error")
	}
}

func TestSetLead_AlreadyLeadNoOp(t *testing.T) {
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

	// Setting same pane again should be idempotent
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead second time: %v", err)
	}
	if w.LeadPaneID != p1.ID {
		t.Errorf("LeadPaneID = %d, want %d", w.LeadPaneID, p1.ID)
	}
}

func TestSetLead_ClampsWidth(t *testing.T) {
	t.Parallel()

	// Single pane at full width, then split to get 2 panes
	p1 := fakePaneID(1)
	p2 := fakePaneID(2)
	w := NewWindow(p1, 100, 24)
	// Split horizontally so both panes are full width
	if _, err := w.SplitRoot(SplitHorizontal, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}

	// p1 is now full width (100). SetLead should clamp to max 80%.
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	left := w.Root.Children[0]
	maxW := w.Width * 4 / 5 // 80%
	if left.W > maxW {
		t.Errorf("lead pane width = %d, want <= %d (80%%)", left.W, maxW)
	}
}

func TestSetLead_ThreeColumnRoot(t *testing.T) {
	t.Parallel()

	// Create a 3-column layout
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
	// Root should have 3 children
	if len(w.Root.Children) != 3 {
		t.Fatalf("pre-lead root children = %d, want 3", len(w.Root.Children))
	}

	// Set p2 (middle) as lead
	if err := w.SetLead(p2.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	// Root should now have exactly 2 children (binary form)
	if len(w.Root.Children) != 2 {
		t.Fatalf("post-lead root children = %d, want 2", len(w.Root.Children))
	}
	if w.Root.Children[0].FindPane(p2.ID) == nil {
		t.Error("lead pane p2 should be in Children[0]")
	}
}

func TestSetLead_ResizesSubtrees(t *testing.T) {
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

	// Verify total width = window width
	left := w.Root.Children[0]
	right := w.Root.Children[1]
	totalW := left.W + 1 + right.W // +1 for separator
	if totalW != w.Width {
		t.Errorf("total width = %d, want %d", totalW, w.Width)
	}

	// Both children should have full height
	if left.H != w.Height {
		t.Errorf("left height = %d, want %d", left.H, w.Height)
	}
	if right.H != w.Height {
		t.Errorf("right height = %d, want %d", right.H, w.Height)
	}
}

func TestUnsetLead_ClearsState(t *testing.T) {
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
		t.Errorf("LeadPaneID = %d, want 0", w.LeadPaneID)
	}
}

func TestUnsetLead_NoLeadErrors(t *testing.T) {
	t.Parallel()

	p1 := fakePaneID(1)
	w := NewWindow(p1, 80, 24)

	err := w.UnsetLead()
	if err == nil {
		t.Fatal("UnsetLead with no lead set should error")
	}
}

func TestIsLeadPane(t *testing.T) {
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

	if !w.IsLeadPane(p1.ID) {
		t.Error("IsLeadPane(p1) should be true")
	}
	if w.IsLeadPane(p2.ID) {
		t.Error("IsLeadPane(p2) should be false")
	}
	if w.IsLeadPane(99) {
		t.Error("IsLeadPane(99) should be false")
	}
}

func TestSplitLeadHorizontal_Blocked(t *testing.T) {
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
	_, err := w.SplitPaneWithOptions(p1.ID, SplitHorizontal, p3, SplitOptions{})
	if err == nil {
		t.Fatal("horizontal split on lead pane should be blocked")
	}
}

func TestSplitLeadVertical_Allowed(t *testing.T) {
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
	_, err := w.SplitPaneWithOptions(p1.ID, SplitVertical, p3, SplitOptions{})
	if err != nil {
		t.Fatalf("vertical split on lead pane should be allowed: %v", err)
	}
}

func TestSplitRootWithLead_GoesToRightSubtree(t *testing.T) {
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

	// Root-level split should go to right subtree, not add a 3rd root child
	p3 := fakePaneID(3)
	if _, err := w.SplitRoot(SplitVertical, p3); err != nil {
		t.Fatalf("SplitRoot with lead: %v", err)
	}

	// Root should still have exactly 2 children (binary invariant)
	if len(w.Root.Children) != 2 {
		t.Errorf("root children = %d, want 2", len(w.Root.Children))
	}

	// p3 should be in the right subtree
	if w.Root.Children[1].FindPane(p3.ID) == nil {
		t.Error("new pane p3 should be in right subtree (Children[1])")
	}
}

func TestCloseLeadPane_ClearsLead(t *testing.T) {
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

	if err := w.ClosePane(p1.ID); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}
	if w.LeadPaneID != 0 {
		t.Errorf("LeadPaneID = %d, want 0 after closing lead", w.LeadPaneID)
	}
}

func TestSwapPanesAcrossLeadColumn_Blocked(t *testing.T) {
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

	err := w.SwapPanes(p1.ID, p2.ID)
	if err == nil {
		t.Fatal("swap across lead column should be blocked")
	}
}

func TestSwapTreeWithLead_Blocked(t *testing.T) {
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

	err := w.SwapTree(p1.ID, p2.ID)
	if err == nil {
		t.Fatal("swap-tree involving lead column should be blocked")
	}
}

func TestMovePaneFromLeadColumn_Blocked(t *testing.T) {
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

	err := w.MovePane(p1.ID, p2.ID, false)
	if err == nil {
		t.Fatal("move from lead column should be blocked")
	}
}

func TestLeadAwareSplitTarget(t *testing.T) {
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

	// Active pane is p2 (set by SplitRoot). Check no redirect.
	w.ActivePane = p2
	target := w.LeadAwareSplitTarget()
	if target != nil {
		t.Errorf("LeadAwareSplitTarget should return nil when active is not lead, got pane %d", target.ID)
	}

	// Active pane is lead p1. Should redirect to first pane in right subtree.
	w.ActivePane = p1
	target = w.LeadAwareSplitTarget()
	if target == nil {
		t.Fatal("LeadAwareSplitTarget should return a redirect target when active is lead")
	}
	if target.ID != p2.ID {
		t.Errorf("LeadAwareSplitTarget = pane %d, want pane %d", target.ID, p2.ID)
	}
}
