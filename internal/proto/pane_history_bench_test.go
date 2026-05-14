package proto

import (
	"bytes"
	"testing"
)

func TestEncodePaneHistoryPayloadDeterministicForIdlePane(t *testing.T) {
	t.Parallel()

	msg := benchmarkPaneHistoryMessage(128, 80)

	first, err := encodePaneHistoryPayload(msg)
	if err != nil {
		t.Fatalf("first encodePaneHistoryPayload: %v", err)
	}
	second, err := encodePaneHistoryPayload(msg)
	if err != nil {
		t.Fatalf("second encodePaneHistoryPayload: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Fatalf("consecutive encodes of unchanged pane history differed")
	}
}

func TestEncodePaneHistoryPayloadCacheHitMatchesFreshEncode(t *testing.T) {
	// Not parallel: testing.AllocsPerRun panics inside parallel tests.
	freshMsg := benchmarkPaneHistoryMessage(128, 80)
	fresh, err := encodePaneHistoryPayload(freshMsg)
	if err != nil {
		t.Fatalf("fresh encodePaneHistoryPayload: %v", err)
	}

	var cache PaneHistoryPayloadCache
	cachedMsg := benchmarkPaneHistoryMessage(128, 80)
	cachedMsg.SetPaneHistoryPayloadCache(&cache, 1)
	if _, err := encodePaneHistoryPayload(cachedMsg); err != nil {
		t.Fatalf("warm cache encodePaneHistoryPayload: %v", err)
	}

	var hit []byte
	allocs := testing.AllocsPerRun(100, func() {
		var err error
		hit, err = encodePaneHistoryPayload(cachedMsg)
		if err != nil {
			t.Fatalf("cached encodePaneHistoryPayload: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("cache-hit allocations = %.1f, want 0", allocs)
	}
	if !bytes.Equal(hit, fresh) {
		t.Fatal("cache-hit payload differed from fresh encode")
	}
}

func TestEncodePaneHistoryPayloadRangeCacheHitMatchesFreshEncode(t *testing.T) {
	// Not parallel: testing.AllocsPerRun panics inside parallel tests.
	freshMsg := benchmarkPaneHistoryMessage(8, 16)
	fresh, err := encodePaneHistoryPayload(freshMsg)
	if err != nil {
		t.Fatalf("fresh encodePaneHistoryPayload: %v", err)
	}

	var cache PaneHistoryPayloadCache
	cachedMsg := benchmarkPaneHistoryMessage(8, 16)
	cachedMsg.SetPaneHistoryPayloadCacheRange(&cache, 1, 10, 18)
	if _, err := encodePaneHistoryPayload(cachedMsg); err != nil {
		t.Fatalf("warm range cache encodePaneHistoryPayload: %v", err)
	}

	var hit []byte
	allocs := testing.AllocsPerRun(100, func() {
		var err error
		hit, err = encodePaneHistoryPayload(cachedMsg)
		if err != nil {
			t.Fatalf("cached range encodePaneHistoryPayload: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("range cache-hit allocations = %.1f, want 0", allocs)
	}
	if !bytes.Equal(hit, fresh) {
		t.Fatal("range cache-hit payload differed from fresh encode")
	}
}

func TestPaneHistoryPayloadRangeCacheMissesAndInvalidates(t *testing.T) {
	t.Parallel()

	var cache PaneHistoryPayloadCache
	if chunks, ok := cache.ChunkPlan(1, 1024); ok || chunks != nil {
		t.Fatalf("empty ChunkPlan = (%v, %v), want nil false", chunks, ok)
	}

	cache.StoreChunkPlan(1, 1024, []PaneHistoryPayloadChunk{{Start: 0, End: 2}})
	chunks, ok := cache.ChunkPlan(1, 1024)
	if !ok || len(chunks) != 1 || chunks[0] != (PaneHistoryPayloadChunk{Start: 0, End: 2}) {
		t.Fatalf("ChunkPlan = (%v, %v), want stored chunk", chunks, ok)
	}
	chunks[0].Start = 99
	chunks, ok = cache.ChunkPlan(1, 1024)
	if !ok || chunks[0].Start != 0 {
		t.Fatalf("ChunkPlan reused caller-mutated slice: (%v, %v)", chunks, ok)
	}
	if chunks, ok := cache.ChunkPlan(1, 2048); ok || chunks != nil {
		t.Fatalf("ChunkPlan with different size = (%v, %v), want nil false", chunks, ok)
	}
	if chunks, ok := cache.ChunkPlan(2, 1024); ok || chunks != nil {
		t.Fatalf("ChunkPlan with different version = (%v, %v), want nil false", chunks, ok)
	}

	msg := benchmarkPaneHistoryMessage(2, 8)
	msg.SetPaneHistoryPayloadCacheRange(&cache, 2, 0, 2)
	if _, err := encodePaneHistoryPayload(msg); err != nil {
		t.Fatalf("range cache encode after version change: %v", err)
	}
	if chunks, ok := cache.ChunkPlan(1, 1024); ok || chunks != nil {
		t.Fatalf("old ChunkPlan after version change = (%v, %v), want nil false", chunks, ok)
	}
}

func TestPaneHistoryPayloadRangeCacheNilReceivers(t *testing.T) {
	t.Parallel()

	var nilMsg *Message
	nilMsg.SetPaneHistoryPayloadCacheRange(&PaneHistoryPayloadCache{}, 1, 0, 1)

	var nilCache *PaneHistoryPayloadCache
	nilCache.StoreChunkPlan(1, 1024, []PaneHistoryPayloadChunk{{Start: 0, End: 1}})
	if chunks, ok := nilCache.ChunkPlan(1, 1024); ok || chunks != nil {
		t.Fatalf("nil ChunkPlan = (%v, %v), want nil false", chunks, ok)
	}

	msg := benchmarkPaneHistoryMessage(1, 4)
	msg.SetPaneHistoryPayloadCacheRange(nil, 1, 0, 1)
	if _, err := encodePaneHistoryPayload(msg); err != nil {
		t.Fatalf("nil range cache encodePaneHistoryPayload: %v", err)
	}
}

func BenchmarkEncodePaneHistoryPayload(b *testing.B) {
	msg := benchmarkPaneHistoryMessage(10_000, 80)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodePaneHistoryPayload(msg); err != nil {
			b.Fatalf("encodePaneHistoryPayload: %v", err)
		}
	}
}

func BenchmarkEncodePaneHistoryPayloadCacheHit(b *testing.B) {
	msg := benchmarkPaneHistoryMessage(10_000, 80)
	var cache PaneHistoryPayloadCache
	msg.SetPaneHistoryPayloadCache(&cache, 1)
	if _, err := encodePaneHistoryPayload(msg); err != nil {
		b.Fatalf("warm cache encodePaneHistoryPayload: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodePaneHistoryPayload(msg); err != nil {
			b.Fatalf("encodePaneHistoryPayload: %v", err)
		}
	}
}
