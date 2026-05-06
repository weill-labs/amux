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
