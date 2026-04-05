package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

type recordingUndoTimer struct {
	stopped bool
}

func (t *recordingUndoTimer) Stop() bool {
	t.stopped = true
	return true
}

func testUndoPane(id uint32) *mux.Pane {
	return newProxyPane(id, mux.PaneMeta{
		Name:  "pane-test",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 24, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
}

func TestNewUndoManagerGracePeriod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  undoManagerConfig
		want time.Duration
	}{
		{
			name: "defaults to config",
			cfg:  undoManagerConfig{},
			want: config.UndoGracePeriod,
		},
		{
			name: "uses override",
			cfg: undoManagerConfig{
				gracePeriod: 42 * time.Millisecond,
			},
			want: 42 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager := newUndoManager(tt.cfg)
			if manager.gracePeriod != tt.want {
				t.Fatalf("gracePeriod = %v, want %v", manager.gracePeriod, tt.want)
			}
		})
	}
}

func TestUndoManagerTrackSoftClosedPaneSchedulesExpiry(t *testing.T) {
	t.Parallel()

	var (
		gotDelay time.Duration
		timer    *recordingUndoTimer
		expired  []uint32
	)

	manager := newUndoManager(undoManagerConfig{
		gracePeriod: 55 * time.Millisecond,
		afterFunc: func(delay time.Duration, fn func()) undoTimer {
			gotDelay = delay
			timer = &recordingUndoTimer{}
			fn()
			return timer
		},
	})

	pane := testUndoPane(7)
	manager.trackSoftClosedPane(pane, func(paneID uint32) {
		expired = append(expired, paneID)
	})

	if gotDelay != 55*time.Millisecond {
		t.Fatalf("trackSoftClosedPane() delay = %v, want %v", gotDelay, 55*time.Millisecond)
	}
	if got := manager.closedPaneCount(); got != 1 {
		t.Fatalf("closedPaneCount() = %d, want 1", got)
	}
	if len(expired) != 1 || expired[0] != pane.ID {
		t.Fatalf("expiry callback = %v, want [%d]", expired, pane.ID)
	}
	if timer == nil {
		t.Fatal("trackSoftClosedPane() did not create a timer")
	}
}

func TestUndoManagerHandlePaneExitFinalizesSoftClosedPane(t *testing.T) {
	t.Parallel()

	manager := newUndoManager(undoManagerConfig{})
	pane := testUndoPane(9)
	manager.closedPanes = []closedPaneRecord{{pane: pane}}
	manager.closedPaneTimers[pane.ID] = &recordingUndoTimer{}

	var closed *mux.Pane
	if handled := manager.handlePaneExit(pane.ID, func(p *mux.Pane) {
		closed = p
	}); !handled {
		t.Fatal("handlePaneExit() should report handled for a soft-closed pane")
	}
	if closed != pane {
		t.Fatalf("closed pane = %v, want %v", closed, pane)
	}
	if got := manager.closedPaneCount(); got != 0 {
		t.Fatalf("closedPaneCount() = %d, want 0", got)
	}
}

func TestUndoManagerHandlePaneCleanupTimeoutFinalizesPane(t *testing.T) {
	t.Parallel()

	manager := newUndoManager(undoManagerConfig{})
	pane := testUndoPane(11)

	var (
		signaled []uint32
		finalize struct {
			paneID    uint32
			closePane bool
			reason    string
		}
	)

	manager.handlePaneCleanupTimeout(
		pane.ID,
		func(id uint32) *mux.Pane {
			if id != pane.ID {
				t.Fatalf("findPaneByID() id = %d, want %d", id, pane.ID)
			}
			return pane
		},
		func(p *mux.Pane) error {
			signaled = append(signaled, p.ID)
			return nil
		},
		func(paneID uint32, closePane bool, reason string) {
			finalize.paneID = paneID
			finalize.closePane = closePane
			finalize.reason = reason
		},
	)

	if len(signaled) != 1 || signaled[0] != pane.ID {
		t.Fatalf("signal callback = %v, want [%d]", signaled, pane.ID)
	}
	if finalize.paneID != pane.ID || !finalize.closePane || finalize.reason != "cleanup timeout" {
		t.Fatalf("finalize callback = %#v, want paneID=%d closePane=true reason=%q", finalize, pane.ID, "cleanup timeout")
	}
}
