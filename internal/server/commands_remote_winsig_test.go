package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestRemoteWindowSignatureIgnoresNonStructuralChanges(t *testing.T) {
	t.Parallel()

	base := proto.WindowSnapshot{
		Name: "amux",
		Root: proto.CellSnapshot{
			W: 200, H: 50, Dir: 0,
			Children: []proto.CellSnapshot{
				{IsLeaf: true, PaneID: 1, W: 100, H: 50},
				{IsLeaf: true, PaneID: 2, W: 100, H: 50},
			},
		},
		Panes: []proto.PaneSnapshot{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}},
	}

	// Same structure, different cell sizes / IDs but same names+shape -> same sig.
	resized := base
	resized.Root.Children = []proto.CellSnapshot{
		{IsLeaf: true, PaneID: 1, W: 150, H: 50},
		{IsLeaf: true, PaneID: 2, W: 50, H: 50},
	}

	if remoteWindowSignature(base) != remoteWindowSignature(resized) {
		t.Fatal("resize-only change should not alter the structural signature")
	}
}

func TestRemoteWindowSignatureDetectsStructuralChanges(t *testing.T) {
	t.Parallel()

	twoPanes := proto.WindowSnapshot{
		Name: "amux",
		Root: proto.CellSnapshot{
			Dir: 0,
			Children: []proto.CellSnapshot{
				{IsLeaf: true, PaneID: 1, W: 100, H: 50},
				{IsLeaf: true, PaneID: 2, W: 100, H: 50},
			},
		},
		Panes: []proto.PaneSnapshot{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}},
	}

	tests := []struct {
		name  string
		other proto.WindowSnapshot
	}{
		{
			name: "added pane",
			other: proto.WindowSnapshot{
				Root: proto.CellSnapshot{Dir: 0, Children: []proto.CellSnapshot{
					{IsLeaf: true, PaneID: 1}, {IsLeaf: true, PaneID: 2}, {IsLeaf: true, PaneID: 3},
				}},
				Panes: []proto.PaneSnapshot{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}, {ID: 3, Name: "c"}},
			},
		},
		{
			name: "removed pane",
			other: proto.WindowSnapshot{
				Root:  proto.CellSnapshot{IsLeaf: true, PaneID: 1},
				Panes: []proto.PaneSnapshot{{ID: 1, Name: "a"}},
			},
		},
		{
			name: "different split direction",
			other: proto.WindowSnapshot{
				Root: proto.CellSnapshot{Dir: 1, Children: []proto.CellSnapshot{
					{IsLeaf: true, PaneID: 1}, {IsLeaf: true, PaneID: 2},
				}},
				Panes: []proto.PaneSnapshot{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if remoteWindowSignature(twoPanes) == remoteWindowSignature(tt.other) {
				t.Fatalf("%s should change the structural signature", tt.name)
			}
		})
	}
}
