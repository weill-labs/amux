package server

import (
	"fmt"
	"testing"
	"time"
)

func TestPaneLogKeepsLastEntriesInOrder(t *testing.T) {
	t.Parallel()

	log := newPaneLog(3)
	base := time.Date(2026, time.March, 23, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		log.Append(PaneLogEntry{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Event:     paneLogEventCreate,
			PaneID:    uint32(i),
			PaneName:  fmt.Sprintf("pane-%d", i),
			Host:      "local",
		})
	}

	got := log.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len(snapshot) = %d, want 3", len(got))
	}

	for i, wantName := range []string{"pane-3", "pane-4", "pane-5"} {
		if got[i].PaneName != wantName {
			t.Fatalf("snapshot[%d].PaneName = %q, want %q", i, got[i].PaneName, wantName)
		}
	}
}

func TestPaneLogExitReason(t *testing.T) {
	t.Parallel()

	log := newPaneLog(10)
	log.Append(PaneLogEntry{
		Event:      paneLogEventCreate,
		PaneName:   "pane-1",
		Host:       "local",
		ExitReason: "",
	})
	log.Append(PaneLogEntry{
		Event:      paneLogEventExit,
		PaneName:   "pane-1",
		Host:       "local",
		ExitReason: "exit 0",
	})

	got := log.Snapshot()
	if len(got) != 2 {
		t.Fatalf("len(snapshot) = %d, want 2", len(got))
	}
	if got[0].ExitReason != "" {
		t.Errorf("create entry reason = %q, want empty", got[0].ExitReason)
	}
	if got[1].ExitReason != "exit 0" {
		t.Errorf("exit entry reason = %q, want %q", got[1].ExitReason, "exit 0")
	}
}

func TestPaneLogNilSafe(t *testing.T) {
	t.Parallel()

	var log *PaneLog
	log.Append(PaneLogEntry{Event: paneLogEventCreate}) // must not panic
	if got := log.Snapshot(); got != nil {
		t.Fatalf("nil log snapshot = %v, want nil", got)
	}
}
