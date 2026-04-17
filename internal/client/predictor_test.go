package client

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestPredictorAllowPredictionHeuristics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ctx     panePredictionContext
		prime   []bool
		pressed []time.Time
		data    []byte
		want    bool
	}{
		{
			name: "shell mode after confirming acks",
			ctx: panePredictionContext{
				Width:       20,
				Height:      3,
				CursorCol:   2,
				CursorRow:   0,
				AltScreen:   false,
				CursorStyle: "block",
			},
			prime: []bool{true, true, true},
			data:  []byte("x"),
			want:  true,
		},
		{
			name: "alt screen requires insert cursor",
			ctx: panePredictionContext{
				Width:       20,
				Height:      6,
				CursorCol:   2,
				CursorRow:   2,
				AltScreen:   true,
				CursorStyle: "block",
			},
			prime: []bool{true, true, true},
			data:  []byte("x"),
			want:  false,
		},
		{
			name: "last rows suppressed",
			ctx: panePredictionContext{
				Width:       20,
				Height:      6,
				CursorCol:   1,
				CursorRow:   4,
				AltScreen:   false,
				CursorStyle: "block",
			},
			prime: []bool{true, true, true},
			data:  []byte("x"),
			want:  false,
		},
		{
			name: "non confirming history suppresses",
			ctx: panePredictionContext{
				Width:       20,
				Height:      3,
				CursorCol:   2,
				CursorRow:   0,
				AltScreen:   false,
				CursorStyle: "block",
			},
			prime: []bool{true, false, true},
			data:  []byte("x"),
			want:  false,
		},
		{
			name: "paste burst suppressed",
			ctx: panePredictionContext{
				Width:       20,
				Height:      3,
				CursorCol:   2,
				CursorRow:   0,
				AltScreen:   false,
				CursorStyle: "block",
			},
			prime: []bool{true, true, true},
			pressed: []time.Time{
				time.Unix(0, 0),
				time.Unix(0, 5*time.Millisecond.Nanoseconds()),
				time.Unix(0, 10*time.Millisecond.Nanoseconds()),
			},
			data: []byte("x"),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newPredictor(localEchoModeAuto, localEchoStyleDim)
			for _, confirming := range tt.prime {
				p.recordAck(1, confirming)
			}
			p.panes[1] = &panePredictorState{recentPresses: append([]time.Time(nil), tt.pressed...)}

			if got := p.allowPrediction(1, tt.ctx, tt.data, time.Unix(0, 15*time.Millisecond.Nanoseconds())); got != tt.want {
				t.Fatalf("allowPrediction() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestPredictorReconcileMatchAndDivergence(t *testing.T) {
	t.Parallel()

	t.Run("match promotes and clears acknowledged epochs", func(t *testing.T) {
		t.Parallel()

		p := newPredictor(localEchoModeAlways, localEchoStyleDim)
		base := capturePredictionBase(t, "$ ")

		firstEpoch, ok := p.predict(1, base, []byte("a"), time.Unix(0, 0))
		if !ok {
			t.Fatal("predict(first) = false, want true")
		}
		secondEpoch, ok := p.predict(1, base, []byte("b"), time.Unix(0, time.Millisecond.Nanoseconds()))
		if !ok {
			t.Fatal("predict(second) = false, want true")
		}
		if secondEpoch <= firstEpoch {
			t.Fatalf("epochs = (%d,%d), want increasing", firstEpoch, secondEpoch)
		}

		confirmed := capturePredictionSnapshotFromText(t, "$ ab")
		result := p.reconcile(1, confirmed, secondEpoch)
		if !result.Matched {
			t.Fatalf("reconcile matched = false, want true: %+v", result)
		}
		if result.Pending != 0 {
			t.Fatalf("pending = %d, want 0", result.Pending)
		}
	})

	t.Run("divergence rebuilds shadow from confirmed truth and later epochs", func(t *testing.T) {
		t.Parallel()

		p := newPredictor(localEchoModeAlways, localEchoStyleDim)
		base := capturePredictionBase(t, "$ ")

		firstEpoch, ok := p.predict(1, base, []byte("{"), time.Unix(0, 0))
		if !ok {
			t.Fatal("predict(first) = false, want true")
		}
		secondEpoch, ok := p.predict(1, base, []byte("x"), time.Unix(0, time.Millisecond.Nanoseconds()))
		if !ok {
			t.Fatal("predict(second) = false, want true")
		}

		confirmed := capturePredictionSnapshotFromText(t, "$ {\n\tx")
		result := p.reconcile(1, confirmed, firstEpoch)
		if result.Matched {
			t.Fatalf("reconcile matched = true, want false: %+v", result)
		}
		if result.Pending != 1 {
			t.Fatalf("pending = %d, want 1", result.Pending)
		}
		shadow, ok := p.shadow(1)
		if !ok {
			t.Fatal("shadow(1) = missing after rebuild")
		}
		if got := shadow.ScreenContains("{x"); !got {
			t.Fatalf("rebuilt shadow should retain later prediction, got %q", shadow.Render())
		}
		if secondEpoch <= firstEpoch {
			t.Fatalf("epochs = (%d,%d), want increasing", firstEpoch, secondEpoch)
		}
	})
}

func capturePredictionBase(t *testing.T, text string) panePredictionBase {
	t.Helper()

	emu := mux.NewVTEmulatorWithDrainAndScrollback(20, 3, mux.DefaultScrollbackLines)
	t.Cleanup(func() { _ = emu.Close() })
	if _, err := emu.Write([]byte(text)); err != nil {
		t.Fatalf("emu.Write: %v", err)
	}
	return panePredictionBase{
		Width:  20,
		Height: 3,
		Screen: mux.RenderWithCursor(emu),
	}
}

func capturePredictionSnapshotFromText(t *testing.T, text string) panePredictionSnapshot {
	t.Helper()

	emu := mux.NewVTEmulatorWithDrainAndScrollback(20, 3, mux.DefaultScrollbackLines)
	t.Cleanup(func() { _ = emu.Close() })
	if _, err := emu.Write([]byte(text)); err != nil {
		t.Fatalf("emu.Write: %v", err)
	}
	return capturePanePredictionSnapshot(emu)
}
