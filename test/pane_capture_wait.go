package test

import (
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func waitForPaneCaptureJSONWithLayout(
	capture func() (proto.CapturePane, bool),
	generation func() uint64,
	waitLayout func(afterGen uint64, timeout time.Duration) bool,
	timeout time.Duration,
) (proto.CapturePane, bool) {
	deadline := time.Now().Add(timeout)
	gen := generation()
	for time.Now().Before(deadline) {
		got, ok := capture()
		if ok {
			return got, true
		}

		waitFor := time.Until(deadline)
		if waitFor > 250*time.Millisecond {
			waitFor = 250 * time.Millisecond
		}
		if waitFor <= 0 {
			waitFor = time.Millisecond
		}
		if !waitLayout(gen, waitFor) {
			continue
		}
		gen = generation()
	}

	return capture()
}
