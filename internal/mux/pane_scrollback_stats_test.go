package mux

import (
	"fmt"
	"testing"
)

func TestPaneScrollbackStatsCountsResidentBaseAndLiveHistory(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(20, 2, 3)
	pane := &Pane{
		ID:              1,
		Meta:            PaneMeta{Name: "pane-1", Host: DefaultHost},
		emulator:        emu,
		scrollbackLines: 3,
		scrollbackLimit: 3,
	}
	pane.baseHistory.Store(&paneBaseHistory{})
	pane.SetRetainedHistory([]string{"base-1", "base-2", "base-3", "base-4"})
	for line := 1; line <= 4; line++ {
		if _, err := emu.Write([]byte(fmt.Sprintf("live-%d\r\n", line))); err != nil {
			t.Fatalf("Write live line %d: %v", line, err)
		}
	}

	stats := pane.ScrollbackStats()

	if stats.PaneID != 1 || stats.PaneName != "pane-1" || stats.Host != DefaultHost {
		t.Fatalf("pane identity = id %d name %q host %q, want pane-1 on local", stats.PaneID, stats.PaneName, stats.Host)
	}
	if stats.Width != 20 || stats.Height != 2 || stats.LimitLines != 3 {
		t.Fatalf("terminal bounds/limit = %dx%d limit %d, want 20x2 limit 3", stats.Width, stats.Height, stats.LimitLines)
	}
	if stats.BaseLines != 3 || stats.LiveLines != 3 {
		t.Fatalf("base/live lines = %d/%d, want 3/3", stats.BaseLines, stats.LiveLines)
	}
	if stats.TotalLines != 6 || stats.EffectiveLines != 3 {
		t.Fatalf("total/effective lines = %d/%d, want resident 6 with effective limit 3", stats.TotalLines, stats.EffectiveLines)
	}
	if stats.BaseBytes == 0 || stats.LiveBytes == 0 || stats.ScreenBytes == 0 || stats.EstimatedBytes == 0 {
		t.Fatalf("expected non-zero byte estimates, got base=%d live=%d screen=%d total=%d", stats.BaseBytes, stats.LiveBytes, stats.ScreenBytes, stats.EstimatedBytes)
	}
	if stats.EstimatedBytes != stats.BaseBytes+stats.LiveBytes+stats.ScreenBytes {
		t.Fatalf("estimated bytes = %d, want base+live+screen = %d", stats.EstimatedBytes, stats.BaseBytes+stats.LiveBytes+stats.ScreenBytes)
	}
}
