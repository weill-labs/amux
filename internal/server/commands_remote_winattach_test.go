package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestPlanRemoteWindowLeaves(t *testing.T) {
	t.Parallel()

	// A vertical split: two leaves side by side, each 100x50.
	ws := proto.WindowSnapshot{
		ID:   7,
		Name: "amux",
		Root: proto.CellSnapshot{
			W: 200, H: 50, Dir: 0,
			Children: []proto.CellSnapshot{
				{IsLeaf: true, PaneID: 11, W: 100, H: 50},
				{IsLeaf: true, PaneID: 12, W: 100, H: 50},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 11, Name: "pane-11"},
			{ID: 12, Name: "pane-12"},
		},
	}

	leaves, err := planRemoteWindowLeaves(ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(leaves) != 2 {
		t.Fatalf("got %d leaves, want 2", len(leaves))
	}
	if leaves[0].remoteID != 11 || leaves[0].name != "pane-11" || leaves[0].cols != 100 {
		t.Fatalf("leaf[0] = %+v", leaves[0])
	}
	if leaves[1].remoteID != 12 || leaves[1].name != "pane-12" {
		t.Fatalf("leaf[1] = %+v", leaves[1])
	}
}

func TestPlanRemoteWindowLeavesErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ws   proto.WindowSnapshot
	}{
		{
			name: "no leaves",
			ws:   proto.WindowSnapshot{ID: 1, Root: proto.CellSnapshot{W: 80, H: 24}},
		},
		{
			name: "leaf without pane snapshot",
			ws: proto.WindowSnapshot{
				ID:   1,
				Root: proto.CellSnapshot{IsLeaf: true, PaneID: 99, W: 80, H: 24},
			},
		},
		{
			name: "leaf with empty name",
			ws: proto.WindowSnapshot{
				ID:    1,
				Root:  proto.CellSnapshot{IsLeaf: true, PaneID: 5, W: 80, H: 24},
				Panes: []proto.PaneSnapshot{{ID: 5, Name: ""}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := planRemoteWindowLeaves(tt.ws); err == nil {
				t.Fatalf("expected error for %s", tt.name)
			}
		})
	}
}
