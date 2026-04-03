package test

import (
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func waitForCaptureJSONWithLayout(
	capture func() proto.CaptureJSON,
	generation func() uint64,
	waitLayout func(afterGen uint64, timeout time.Duration) bool,
	fn func(proto.CaptureJSON) bool,
	timeout time.Duration,
) (proto.CaptureJSON, bool) {
	deadline := time.Now().Add(timeout)
	gen := generation()
	for time.Now().Before(deadline) {
		got := capture()
		if fn(got) {
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

	got := capture()
	return got, fn(got)
}
