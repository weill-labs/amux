package client

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

const (
	clientFrameStatsWindowSize = 2048
	clientFrameStatsRecentSize = 100
)

type clientFrameSample struct {
	frameDuration   time.Duration
	cellsDiffed     int
	ansiBytes       int
	panesComposited int
}

type clientFrameStats struct {
	samples [clientFrameStatsWindowSize]clientFrameSample
	next    int
	count   int
}

type clientFrameStatsSnapshot struct {
	sampleCount int
	samples     []clientFrameSample
}

func (s *clientFrameStats) record(sample clientFrameSample) {
	s.samples[s.next] = sample
	s.next = (s.next + 1) % len(s.samples)
	if s.count < len(s.samples) {
		s.count++
	}
}

func (s *clientFrameStats) snapshot() clientFrameStatsSnapshot {
	if s.count == 0 {
		return clientFrameStatsSnapshot{}
	}

	start := s.next - s.count
	if start < 0 {
		start += len(s.samples)
	}

	out := make([]clientFrameSample, 0, s.count)
	for i := 0; i < s.count; i++ {
		idx := (start + i) % len(s.samples)
		out = append(out, s.samples[idx])
	}
	return clientFrameStatsSnapshot{
		sampleCount: s.count,
		samples:     out,
	}
}

func (s *clientFrameStats) format() string {
	snap := s.snapshot()
	if snap.sampleCount == 0 {
		return "no frame samples recorded yet"
	}

	durations := make([]time.Duration, 0, len(snap.samples))
	cells := make([]int, 0, len(snap.samples))
	ansiBytes := make([]int, 0, len(snap.samples))
	panes := make([]int, 0, len(snap.samples))
	for _, sample := range snap.samples {
		durations = append(durations, sample.frameDuration)
		cells = append(cells, sample.cellsDiffed)
		ansiBytes = append(ansiBytes, sample.ansiBytes)
		panes = append(panes, sample.panesComposited)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "samples: %d\n", snap.sampleCount)
	fmt.Fprintf(&b, "frame duration: p50 %s  p95 %s  p99 %s\n",
		percentileDuration(durations, 50),
		percentileDuration(durations, 95),
		percentileDuration(durations, 99),
	)
	fmt.Fprintf(&b, "cells diffed: p50 %d  p95 %d  p99 %d\n",
		percentileInt(cells, 50),
		percentileInt(cells, 95),
		percentileInt(cells, 99),
	)
	fmt.Fprintf(&b, "ansi bytes emitted: p50 %d  p95 %d  p99 %d\n",
		percentileInt(ansiBytes, 50),
		percentileInt(ansiBytes, 95),
		percentileInt(ansiBytes, 99),
	)
	fmt.Fprintf(&b, "panes composited: p50 %d  p95 %d  p99 %d\n",
		percentileInt(panes, 50),
		percentileInt(panes, 95),
		percentileInt(panes, 99),
	)

	recentStart := len(snap.samples) - clientFrameStatsRecentSize
	if recentStart < 0 {
		recentStart = 0
	}
	b.WriteString("last 100 frame times (oldest -> newest): ")
	for i, sample := range snap.samples[recentStart:] {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(sample.frameDuration.String())
	}
	return b.String()
}

func percentileDuration(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	slices.Sort(sorted)
	return sorted[percentileIndex(len(sorted), percentile)]
}

func percentileInt(values []int, percentile int) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	slices.Sort(sorted)
	return sorted[percentileIndex(len(sorted), percentile)]
}

func percentileIndex(n, percentile int) int {
	if n <= 0 {
		return 0
	}
	if percentile <= 0 {
		return 0
	}
	if percentile >= 100 {
		return n - 1
	}
	// Nearest-rank percentile with 1-based rank converted back to 0-based.
	rank := (n*percentile + 99) / 100
	idx := rank - 1
	if idx < 0 {
		return 0
	}
	if idx >= n {
		return n - 1
	}
	return idx
}

func isDebugFramesClientQuery(args []string) bool {
	return len(args) == 1 && args[0] == proto.ClientQueryDebugFramesArg
}

func clientFrameStatsResponse(stats clientFrameStats) *proto.Message {
	return &proto.Message{
		Type:      proto.MsgTypeCaptureResponse,
		CmdOutput: stats.format() + "\n",
	}
}
