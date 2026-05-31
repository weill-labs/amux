package server

import (
	"context"
	"errors"
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

func TestSnapshotPaneHistoryScreensContextReturnsDeadlineExceeded(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := snapshotPaneHistoryScreensContext(ctx, []*mux.Pane{{ID: 1}}, func(*mux.Pane) ([]string, string, uint64) {
		time.Sleep(time.Second)
		return []string{"late"}, "late", 0
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshotPaneHistoryScreensContext() error = %v, want context deadline exceeded", err)
	}
}
