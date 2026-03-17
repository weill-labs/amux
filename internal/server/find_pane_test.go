package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestSessionFindPaneByRef(t *testing.T) {
	t.Parallel()

	sess := newSession("test-find")

	// Create mock panes in the flat registry (not in any window layout)
	panes := []struct {
		id   uint32
		name string
	}{
		{1, "pane-1"},
		{2, "pane-2"},
		{10, "agent-task"},
	}
	for _, p := range panes {
		sess.Panes = append(sess.Panes, &mux.Pane{
			ID:   p.id,
			Meta: mux.PaneMeta{Name: p.name},
		})
	}

	tests := []struct {
		name   string
		ref    string
		wantID uint32
	}{
		{"exact name", "pane-1", 1},
		{"exact name 2", "agent-task", 10},
		{"numeric ID", "2", 2},
		{"numeric ID 10", "10", 10},
		{"prefix match", "pane-", 1}, // first prefix match
		{"prefix match agent", "agent", 10},
		{"no match", "nonexistent", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sess.findPaneByRef(tt.ref)
			if tt.wantID == 0 {
				if got != nil {
					t.Errorf("findPaneByRef(%q) = pane %d, want nil", tt.ref, got.ID)
				}
				return
			}
			if got == nil {
				t.Fatalf("findPaneByRef(%q) = nil, want pane %d", tt.ref, tt.wantID)
			}
			if got.ID != tt.wantID {
				t.Errorf("findPaneByRef(%q) = pane %d, want pane %d", tt.ref, got.ID, tt.wantID)
			}
		})
	}
}
