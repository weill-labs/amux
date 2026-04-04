package client

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestClientFrameStatsFormatIncludesPercentilesAndRecentFrames(t *testing.T) {
	t.Parallel()

	var stats clientFrameStats
	for i := 1; i <= 120; i++ {
		stats.record(clientFrameSample{
			frameDuration:   time.Duration(i) * time.Millisecond,
			cellsDiffed:     i,
			ansiBytes:       i * 10,
			panesComposited: (i % 3) + 1,
		})
	}

	got := stats.format()

	for _, want := range []string{
		"samples: 120",
		"frame duration",
		"p50 60ms",
		"p95 114ms",
		"p99 119ms",
		"cells diffed",
		"ansi bytes emitted",
		"panes composited",
		"last 100 frame times (oldest -> newest): 21ms, 22ms",
		"119ms, 120ms",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("format missing %q:\n%s", want, got)
		}
	}
}

func TestRenderNowRecordsFrameStats(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	state := &clientRenderLoopState{renderFrameInterval: time.Millisecond}

	cr.renderNow(state, func(string) {})

	snap := cr.frameStats.snapshot()
	if snap.sampleCount != 1 {
		t.Fatalf("sampleCount = %d, want 1", snap.sampleCount)
	}
	if len(snap.samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1", len(snap.samples))
	}

	sample := snap.samples[0]
	if sample.frameDuration < 0 {
		t.Fatalf("frameDuration = %v, want non-negative", sample.frameDuration)
	}
	if sample.cellsDiffed != 80*24 {
		t.Fatalf("cellsDiffed = %d, want %d", sample.cellsDiffed, 80*24)
	}
	if sample.ansiBytes <= 0 {
		t.Fatalf("ansiBytes = %d, want > 0", sample.ansiBytes)
	}
	if sample.panesComposited != 2 {
		t.Fatalf("panesComposited = %d, want 2", sample.panesComposited)
	}
}

func TestHandleCaptureRequestDebugFrames(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	state := &clientRenderLoopState{renderFrameInterval: time.Millisecond}
	cr.renderNow(state, func(string) {})

	resp := cr.HandleCaptureRequest([]string{proto.ClientQueryDebugFramesArg}, nil)
	if resp.Type != proto.MsgTypeCaptureResponse {
		t.Fatalf("type = %d, want %d", resp.Type, proto.MsgTypeCaptureResponse)
	}
	if resp.CmdErr != "" {
		t.Fatalf("CmdErr = %q, want empty", resp.CmdErr)
	}
	for _, want := range []string{"frame duration", "last 100 frame times"} {
		if !strings.Contains(resp.CmdOutput, want) {
			t.Fatalf("debug frames output missing %q:\n%s", want, resp.CmdOutput)
		}
	}
}
