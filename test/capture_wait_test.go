package test

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestWaitForCaptureJSONWithLayoutWaitsForNextGeneration(t *testing.T) {
	t.Parallel()

	gen := uint64(7)
	waitCalls := 0
	captureCalls := 0
	ready := false

	got, ok := waitForCaptureJSONWithLayout(
		func() proto.CaptureJSON {
			captureCalls++
			x := 40
			if ready {
				x = 0
			}
			return proto.CaptureJSON{
				Panes: []proto.CapturePane{{
					Name:     "pane-2",
					Position: &proto.CapturePos{X: x, Y: 0, Width: 40, Height: 22},
				}},
			}
		},
		func() uint64 { return gen },
		func(afterGen uint64, timeout time.Duration) bool {
			waitCalls++
			if afterGen != 7 {
				t.Fatalf("waitLayout(after=%d), want 7", afterGen)
			}
			if timeout <= 0 {
				t.Fatalf("waitLayout timeout = %v, want > 0", timeout)
			}
			ready = true
			gen = 8
			return true
		},
		func(capture proto.CaptureJSON) bool {
			return capture.Panes[0].Position.X == 0
		},
		200*time.Millisecond,
	)
	if !ok {
		t.Fatal("waitForCaptureJSONWithLayout() = false, want true")
	}
	if got.Panes[0].Position.X != 0 {
		t.Fatalf("returned capture X = %d, want 0", got.Panes[0].Position.X)
	}
	if waitCalls != 1 {
		t.Fatalf("waitLayout calls = %d, want 1", waitCalls)
	}
	if captureCalls != 2 {
		t.Fatalf("capture calls = %d, want 2", captureCalls)
	}
}

func TestWaitForCaptureJSONWithLayoutReturnsLastCaptureOnTimeout(t *testing.T) {
	t.Parallel()

	capture := proto.CaptureJSON{
		Panes: []proto.CapturePane{{
			Name:     "pane-1",
			Position: &proto.CapturePos{X: 12, Y: 0, Width: 40, Height: 22},
		}},
	}

	got, ok := waitForCaptureJSONWithLayout(
		func() proto.CaptureJSON { return capture },
		func() uint64 { return 3 },
		func(afterGen uint64, timeout time.Duration) bool {
			if afterGen != 3 {
				t.Fatalf("waitLayout(after=%d), want 3", afterGen)
			}
			if timeout <= 0 {
				t.Fatalf("waitLayout timeout = %v, want > 0", timeout)
			}
			return false
		},
		func(proto.CaptureJSON) bool { return false },
		50*time.Millisecond,
	)
	if ok {
		t.Fatal("waitForCaptureJSONWithLayout() = true, want false")
	}
	if got.Panes[0].Position.X != capture.Panes[0].Position.X {
		t.Fatalf("returned capture X = %d, want %d", got.Panes[0].Position.X, capture.Panes[0].Position.X)
	}
}
