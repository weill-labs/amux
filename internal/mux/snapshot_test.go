package mux

import (
	"bytes"
	"encoding/gob"
	"reflect"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestSnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: "local", Color: "f38ba8"}}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: "remote", Task: "TASK-1", Color: "a6e3a1"}}
	w := NewWindow(p1, 80, 24)
	leaf2 := NewLeaf(p2, 41, 0, 38, 24)
	leaf2.Parent = w.Root
	w.Root.isLeaf = false
	w.Root.Pane = nil
	w.Root.Dir = SplitVertical
	child1 := NewLeaf(p1, 0, 0, 40, 24)
	child1.Parent = w.Root
	w.Root.Children = []*LayoutCell{child1, leaf2}

	snap := w.SnapshotLayout("test-session")
	if snap.SessionName != "test-session" {
		t.Errorf("SessionName = %q", snap.SessionName)
	}
	if len(snap.Panes) != 2 {
		t.Fatalf("Panes = %d, want 2", len(snap.Panes))
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(snap); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded proto.LayoutSnapshot
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.SessionName != snap.SessionName {
		t.Errorf("decoded SessionName = %q", decoded.SessionName)
	}
	if len(decoded.Root.Children) != 2 {
		t.Fatalf("decoded children = %d", len(decoded.Root.Children))
	}
}

func TestRebuildLayout(t *testing.T) {
	t.Parallel()

	cs := proto.CellSnapshot{
		X: 0, Y: 0, W: 80, H: 24, Dir: 0,
		Children: []proto.CellSnapshot{
			{X: 0, Y: 0, W: 39, H: 24, IsLeaf: true, Dir: -1, PaneID: 1},
			{X: 40, Y: 0, W: 39, H: 24, IsLeaf: true, Dir: -1, PaneID: 2},
		},
	}

	root := RebuildLayout(cs)
	if root.IsLeaf() {
		t.Error("root should not be leaf")
	}
	if len(root.Children) != 2 {
		t.Fatalf("children = %d", len(root.Children))
	}
	if root.Children[0].PaneID != 1 {
		t.Errorf("child0 PaneID = %d", root.Children[0].PaneID)
	}
	if root.Children[0].Parent != root {
		t.Error("child0 parent not set")
	}
}

func TestRebuildFromSnapshot(t *testing.T) {
	t.Parallel()

	// Create panes (no real PTY — just enough for layout reconstruction)
	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: "local", Color: "f38ba8"}}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: "remote", Color: "a6e3a1"}}
	p3 := &Pane{ID: 3, Meta: PaneMeta{Name: "pane-3", Host: "local", Color: "cba6f7"}}

	// 2x2 layout: vertical split at root, horizontal split in left child
	snap := proto.LayoutSnapshot{
		SessionName:  "test",
		ActivePaneID: 2,
		Width:        80,
		Height:       24,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 24, Dir: 0, // SplitVertical
			Children: []proto.CellSnapshot{
				{
					X: 0, Y: 0, W: 39, H: 24, Dir: 1, // SplitHorizontal
					Children: []proto.CellSnapshot{
						{X: 0, Y: 0, W: 39, H: 11, IsLeaf: true, Dir: -1, PaneID: 1},
						{X: 0, Y: 12, W: 39, H: 11, IsLeaf: true, Dir: -1, PaneID: 3},
					},
				},
				{X: 40, Y: 0, W: 39, H: 24, IsLeaf: true, Dir: -1, PaneID: 2},
			},
		},
	}

	paneMap := map[uint32]*Pane{1: p1, 2: p2, 3: p3}
	w := RebuildFromSnapshot(snap, paneMap)

	// Active pane
	if w.ActivePane != p2 {
		t.Errorf("ActivePane = pane %d, want pane 2", w.ActivePane.ID)
	}

	// Dimensions
	if w.Width != 80 || w.Height != 24 {
		t.Errorf("Size = %dx%d, want 80x24", w.Width, w.Height)
	}

	// Root structure
	if w.Root.IsLeaf() {
		t.Fatal("root should not be leaf")
	}
	if len(w.Root.Children) != 2 {
		t.Fatalf("root children = %d, want 2", len(w.Root.Children))
	}

	// Left child is internal (horizontal split)
	left := w.Root.Children[0]
	if left.IsLeaf() {
		t.Fatal("left child should not be leaf")
	}
	if len(left.Children) != 2 {
		t.Fatalf("left children = %d, want 2", len(left.Children))
	}
	if left.Children[0].Pane != p1 {
		t.Error("left.child0 should point to p1")
	}
	if left.Children[1].Pane != p3 {
		t.Error("left.child1 should point to p3")
	}
	if left.Parent != w.Root {
		t.Error("left.Parent should point to root")
	}

	// Right child is leaf with p2
	right := w.Root.Children[1]
	if !right.IsLeaf() {
		t.Fatal("right child should be leaf")
	}
	if right.Pane != p2 {
		t.Error("right should point to p2")
	}
	if right.Parent != w.Root {
		t.Error("right.Parent should point to root")
	}

	// Verify Panes() walks all leaves
	panes := w.Panes()
	if len(panes) != 3 {
		t.Errorf("Panes() = %d, want 3", len(panes))
	}

	// Verify FindPane works
	cell := w.Root.FindPane(3)
	if cell == nil {
		t.Fatal("FindPane(3) returned nil")
	}
}

func TestRebuildFromSnapshotFallbackActive(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1"}}
	snap := proto.LayoutSnapshot{
		ActivePaneID: 99, // doesn't exist
		Width:        80,
		Height:       24,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 1,
		},
	}

	w := RebuildFromSnapshot(snap, map[uint32]*Pane{1: p1})
	if w.ActivePane == nil {
		t.Fatal("ActivePane should fallback to any pane")
	}
	if w.ActivePane != p1 {
		t.Errorf("ActivePane = pane %d, want 1", w.ActivePane.ID)
	}
}

func TestPaneToSnapshotIncludesMetaCollections(t *testing.T) {
	t.Parallel()

	pane := &Pane{
		ID: 7,
		Meta: PaneMeta{
			Name:      "pane-7",
			Host:      "local",
			Task:      "build",
			Color:     "f38ba8",
			GitBranch: "feat/meta",
			PR:        "99",
		},
	}
	metaValue := reflect.ValueOf(&pane.Meta).Elem()
	prsField := metaValue.FieldByName("TrackedPRs")
	if !prsField.IsValid() {
		t.Fatal("PaneMeta.TrackedPRs field missing")
	}
	prsField.Set(reflect.ValueOf([]proto.TrackedPR{{Number: 42}, {Number: 73}}))
	issuesField := metaValue.FieldByName("TrackedIssues")
	if !issuesField.IsValid() {
		t.Fatal("PaneMeta.TrackedIssues field missing")
	}
	issuesField.Set(reflect.ValueOf([]proto.TrackedIssue{{ID: "LAB-338"}, {ID: "LAB-412"}}))

	snap := pane.ToSnapshot()
	snapValue := reflect.ValueOf(snap)
	gotPRs := snapValue.FieldByName("TrackedPRs")
	if !gotPRs.IsValid() {
		t.Fatal("PaneSnapshot.TrackedPRs field missing")
	}
	if gotPRs.Len() != 2 || gotPRs.Index(0).FieldByName("Number").Int() != 42 || gotPRs.Index(1).FieldByName("Number").Int() != 73 {
		t.Fatalf("snapshot PRs = %#v, want [42 73]", gotPRs.Interface())
	}
	gotIssues := snapValue.FieldByName("TrackedIssues")
	if !gotIssues.IsValid() {
		t.Fatal("PaneSnapshot.TrackedIssues field missing")
	}
	if gotIssues.Len() != 2 || gotIssues.Index(0).FieldByName("ID").String() != "LAB-338" || gotIssues.Index(1).FieldByName("ID").String() != "LAB-412" {
		t.Fatalf("snapshot Issues = %#v, want [LAB-338 LAB-412]", gotIssues.Interface())
	}
}

func TestSnapshotRoundTrip_LeadPaneID(t *testing.T) {
	t.Parallel()

	p1 := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", Host: "local", Color: "f38ba8"}}
	p2 := &Pane{ID: 2, Meta: PaneMeta{Name: "pane-2", Host: "local", Color: "a6e3a1"}}

	w := NewWindow(p1, 80, 24)
	if _, err := w.SplitRoot(SplitVertical, p2); err != nil {
		t.Fatalf("SplitRoot: %v", err)
	}
	if err := w.SetLead(p1.ID); err != nil {
		t.Fatalf("SetLead: %v", err)
	}

	// Snapshot via LayoutSnapshot path
	snap := w.SnapshotLayout("test")
	if snap.LeadPaneID != p1.ID {
		t.Errorf("LayoutSnapshot.LeadPaneID = %d, want %d", snap.LeadPaneID, p1.ID)
	}

	// Check PaneSnapshot.Lead flag
	var foundLead bool
	for _, ps := range snap.Panes {
		if ps.ID == p1.ID && ps.Lead {
			foundLead = true
		}
		if ps.ID == p2.ID && ps.Lead {
			t.Error("p2 should not have Lead=true")
		}
	}
	if !foundLead {
		t.Error("p1 PaneSnapshot should have Lead=true")
	}

	// Rebuild and verify
	paneMap := map[uint32]*Pane{1: p1, 2: p2}
	rebuilt := RebuildFromSnapshot(*snap, paneMap)
	if rebuilt.LeadPaneID != p1.ID {
		t.Errorf("rebuilt LeadPaneID = %d, want %d", rebuilt.LeadPaneID, p1.ID)
	}

	// Also test WindowSnapshot path
	ws := w.SnapshotWindow(1)
	if ws.LeadPaneID != p1.ID {
		t.Errorf("WindowSnapshot.LeadPaneID = %d, want %d", ws.LeadPaneID, p1.ID)
	}
	rebuiltW := RebuildWindowFromSnapshot(ws, 80, 24, paneMap)
	if rebuiltW.LeadPaneID != p1.ID {
		t.Errorf("rebuilt from WindowSnapshot LeadPaneID = %d, want %d", rebuiltW.LeadPaneID, p1.ID)
	}
}
