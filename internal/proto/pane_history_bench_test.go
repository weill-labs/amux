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
