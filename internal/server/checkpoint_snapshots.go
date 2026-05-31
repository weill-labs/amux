package server

import (
	"context"

	"github.com/weill-labs/amux/internal/mux"
)

type paneHistoryScreenSnapshot struct {
	history []string
	screen  string
}

// snapshotPaneHistoryScreens fans out per-pane snapshot work and writes the
// results back into a pre-sized slice by pane index so callers keep sess.Panes
// ordering even when snapshots complete out of order. Production callers pass
// mux.Pane.HistoryScreenSnapshot, which is safe to run concurrently across
// distinct panes because each pane serializes emulator access through its own
// actor goroutine.
func snapshotPaneHistoryScreens(panes []*mux.Pane, snapshot func(*mux.Pane) ([]string, string, uint64)) []paneHistoryScreenSnapshot {
	snapshots, _ := snapshotPaneHistoryScreensContext(context.Background(), panes, snapshot)
	return snapshots
}

func snapshotPaneHistoryScreensContext(ctx context.Context, panes []*mux.Pane, snapshot func(*mux.Pane) ([]string, string, uint64)) ([]paneHistoryScreenSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(panes) == 0 {
		return nil, nil
	}

	type paneSnapshotResult struct {
		index   int
		history []string
		screen  string
	}

	ch := make(chan paneSnapshotResult, len(panes))
	for i, p := range panes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		go func(index int, pane *mux.Pane) {
			history, screen, _ := snapshot(pane)
			ch <- paneSnapshotResult{index: index, history: history, screen: screen}
		}(i, p)
	}

	snapshots := make([]paneHistoryScreenSnapshot, len(panes))
	for range panes {
		select {
		case r := <-ch:
			snapshots[r.index] = paneHistoryScreenSnapshot{
				history: r.history,
				screen:  r.screen,
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return snapshots, nil
}
