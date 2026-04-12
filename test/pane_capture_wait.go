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
	return waitForValueWithLayout(capture, generation, waitLayout, timeout)
}
