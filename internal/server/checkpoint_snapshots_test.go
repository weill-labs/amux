package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestSnapshotPaneHistoryScreensPreservesInputOrder(t *testing.T) {
	t.Parallel()

	panes := []*mux.Pane{
		{ID: 1},
		{ID: 2},
		{ID: 3},
	}
	delays := map[uint32]time.Duration{
		1: 40 * time.Millisecond,
		2: 0,
		3: 10 * time.Millisecond,
	}

	snapshots := snapshotPaneHistoryScreens(panes, func(p *mux.Pane) ([]string, string, uint64) {
		time.Sleep(delays[p.ID])
		return []string{fmt.Sprintf("history-%d", p.ID)}, fmt.Sprintf("screen-%d", p.ID), uint64(p.ID)
	})

	if len(snapshots) != len(panes) {
		t.Fatalf("len(snapshotPaneHistoryScreens(...)) = %d, want %d", len(snapshots), len(panes))
	}

	for i, p := range panes {
		if got := snapshots[i].history; len(got) != 1 || got[0] != fmt.Sprintf("history-%d", p.ID) {
			t.Fatalf("snapshots[%d].history = %v, want [history-%d]", i, got, p.ID)
		}
		if got := snapshots[i].screen; got != fmt.Sprintf("screen-%d", p.ID) {
			t.Fatalf("snapshots[%d].screen = %q, want screen-%d", i, got, p.ID)
		}
	}
}
