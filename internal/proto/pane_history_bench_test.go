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

func BenchmarkEncodePaneHistoryPayload(b *testing.B) {
	msg := benchmarkPaneHistoryMessage(10_000, 80)

	b.ReportAllocs()
	for b.Loop() {
		if _, err := encodePaneHistoryPayload(msg); err != nil {
			b.Fatalf("encodePaneHistoryPayload: %v", err)
		}
	}
}
