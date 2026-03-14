package mux

import (
	"bytes"
	"encoding/gob"
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
	w.Root.Dir = SplitHorizontal
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
