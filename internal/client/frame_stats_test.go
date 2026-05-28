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
		"actor queueing latency",
		"last 100 frame times (oldest -> newest): 21ms, 22ms",
		"119ms, 120ms",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("format missing %q:\n%s", want, got)
		}
	}
}

func TestClientFrameStatsFormatIncludesActorQueueingLatencyPercentiles(t *testing.T) {
	t.Parallel()

	var stats clientFrameStats
	stats.record(clientFrameSample{
		frameDuration:   time.Millisecond,
		cellsDiffed:     10,
		ansiBytes:       100,
		panesComposited: 1,
	})
	for i := 1; i <= 5; i++ {
		stats.recordActorQueueLatency(time.Duration(i) * time.Millisecond)
	}

	got := stats.format()
	if !strings.Contains(got, "actor queueing latency: p50 3ms  p95 5ms  p99 5ms") {
		t.Fatalf("format missing actor queueing latency percentiles:\n%s", got)
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

func TestRenderCoalescedRecordsActorQueueLatency(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	cr.renderFrameInterval = time.Millisecond
	msgCh := make(chan *RenderMsg, 2)
	rendered := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		cr.RenderCoalesced(msgCh, func(string) {
			rendered <- struct{}{}
		})
		close(done)
	}()

	if !sendRenderMsg(msgCh, nil, &RenderMsg{
		Typ:    RenderMsgPaneOutput,
		PaneID: 1,
		Data:   []byte("queued output"),
	}) {
		t.Fatal("sendRenderMsg returned false")
	}

	select {
	case <-rendered:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("queued pane output did not render")
	}

	snap := cr.frameStats.snapshot()
	if snap.actorQueueSampleCount == 0 {
		t.Fatal("actorQueueSampleCount = 0, want queued message latency sample")
	}

	msgCh <- &RenderMsg{Typ: RenderMsgExit}
	close(msgCh)
	<-done
}
