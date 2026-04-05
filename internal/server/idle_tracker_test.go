package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func testClockFn(clock Clock) func() Clock {
	return func() Clock { return clock }
}

func TestIdleTrackerDefaultsAndOverrides(t *testing.T) {
	t.Parallel()

	tracker := NewIdleTracker(testClockFn(RealClock{}))
	if got := tracker.Settle(); got != config.VTIdleSettle {
		t.Fatalf("Settle() = %v, want %v", got, config.VTIdleSettle)
	}
	if got := tracker.Timeout(); got != config.VTIdleTimeout {
		t.Fatalf("Timeout() = %v, want %v", got, config.VTIdleTimeout)
	}

	tracker.VTIdleSettle = 25 * time.Millisecond
	tracker.VTIdleTimeout = 3 * time.Second
	if got := tracker.Settle(); got != 25*time.Millisecond {
		t.Fatalf("Settle() override = %v, want %v", got, 25*time.Millisecond)
	}
	if got := tracker.Timeout(); got != 3*time.Second {
		t.Fatalf("Timeout() override = %v, want %v", got, 3*time.Second)
	}
}

func TestIdleTrackerTrackOutputTransitionsAndCallbacks(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC))
	tracker := NewIdleTracker(testClockFn(clk))
	tracker.VTIdleSettle = 100 * time.Millisecond

	tracker.HandleIdleTimeout(7)
	if !tracker.IsIdle(7) {
		t.Fatal("pane should start idle after HandleIdleTimeout")
	}

	idleTimeouts := 0
	var settled []time.Time
	if wasIdle := tracker.TrackOutput(7, func() { idleTimeouts++ }, func(lastOutput time.Time) {
		settled = append(settled, lastOutput)
	}); !wasIdle {
		t.Fatal("TrackOutput should report an idle->busy transition")
	}
	if tracker.IsIdle(7) {
		t.Fatal("pane should be busy immediately after output")
	}

	lastOutput, ok := tracker.LastOutput(7)
	if !ok || !lastOutput.Equal(clk.Now()) {
		t.Fatalf("LastOutput() = (%v, %v), want (%v, true)", lastOutput, ok, clk.Now())
	}

	clk.AwaitTimers(2)
	clk.Advance(100 * time.Millisecond)

	if idleTimeouts != 1 {
		t.Fatalf("idle timeout callbacks = %d, want 1", idleTimeouts)
	}
	if len(settled) != 1 || !settled[0].Equal(lastOutput) {
		t.Fatalf("settled callbacks = %v, want [%v]", settled, lastOutput)
	}
}

func TestIdleTrackerHandleVTIdleTimeoutRejectsStaleOutput(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC))
	tracker := NewIdleTracker(testClockFn(clk))
	tracker.VTIdleSettle = 100 * time.Millisecond

	tracker.TrackOutput(9, func() {}, func(time.Time) {})
	first, ok := tracker.LastOutput(9)
	if !ok {
		t.Fatal("missing first last-output edge")
	}

	clk.Advance(50 * time.Millisecond)
	tracker.TrackOutput(9, func() {}, func(time.Time) {})
	second, ok := tracker.LastOutput(9)
	if !ok {
		t.Fatal("missing second last-output edge")
	}

	if tracker.HandleVTIdleTimeout(9, first) {
		t.Fatal("stale vt-idle timeout should be ignored")
	}
	if !tracker.HandleVTIdleTimeout(9, second) {
		t.Fatal("fresh vt-idle timeout should settle the pane")
	}
	if tracker.HandleVTIdleTimeout(9, second) {
		t.Fatal("duplicate vt-idle timeout should not settle twice")
	}
}

func TestIdleTrackerSnapshotStateAndStopPane(t *testing.T) {
	t.Parallel()

	clk := NewFakeClock(time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC))
	tracker := NewIdleTracker(testClockFn(clk))
	tracker.VTIdleSettle = 2 * time.Second

	pane1 := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	pane2 := newProxyPane(2, mux.PaneMeta{Name: "pane-2", Host: mux.DefaultHost, Color: config.AccentColor(1)}, 80, 23, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	pane1.SetCreatedAt(clk.Now().Add(-5 * time.Second))
	pane2.SetCreatedAt(clk.Now())
	tracker.TrackOutput(pane2.ID, func() {}, func(time.Time) {})

	snap := tracker.SnapshotState([]*mux.Pane{pane1, pane2}, clk.Now())
	if !snap[pane1.ID] {
		t.Fatal("pane-1 should be idle once createdAt+settle has elapsed")
	}
	if snap[pane2.ID] {
		t.Fatal("pane-2 should remain busy while fresh output is settling")
	}

	tracker.HandleIdleTimeout(pane2.ID)
	snap = tracker.SnapshotState([]*mux.Pane{pane1, pane2}, clk.Now())
	if !snap[pane2.ID] {
		t.Fatal("pane-2 should be idle after the input-idle timeout")
	}

	tracker.StopPane(pane2.ID)
	if got, ok := tracker.LastOutput(pane2.ID); ok || !got.IsZero() {
		t.Fatalf("LastOutput() after StopPane = (%v, %v), want (zero, false)", got, ok)
	}

	stateSnap, sinceSnap := tracker.SnapshotFull()
	if _, ok := stateSnap[pane2.ID]; ok {
		t.Fatal("stopped pane should be removed from idle state snapshot")
	}
	if _, ok := sinceSnap[pane2.ID]; ok {
		t.Fatal("stopped pane should be removed from idle-since snapshot")
	}
}
