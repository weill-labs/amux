package server

import "github.com/weill-labs/amux/internal/mux"

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
	if len(panes) == 0 {
		return nil
	}

	type paneSnapshotResult struct {
		index   int
		history []string
		screen  string
	}

	ch := make(chan paneSnapshotResult, len(panes))
	for i, p := range panes {
		go func(index int, pane *mux.Pane) {
			history, screen, _ := snapshot(pane)
			ch <- paneSnapshotResult{index: index, history: history, screen: screen}
		}(i, p)
	}

	snapshots := make([]paneHistoryScreenSnapshot, len(panes))
	for range panes {
		r := <-ch
		snapshots[r.index] = paneHistoryScreenSnapshot{
			history: r.history,
			screen:  r.screen,
		}
	}

	return snapshots
}
