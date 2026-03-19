package test

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func assertCaptureConsistent(t *testing.T, capture proto.CaptureJSON) {
	t.Helper()

	if capture.Window.ID == 0 {
		t.Fatal("capture window id must be non-zero")
	}
	if len(capture.Panes) == 0 {
		t.Fatal("capture must contain at least one pane")
	}

	seenIDs := make(map[uint32]bool, len(capture.Panes))
	seenNames := make(map[string]bool, len(capture.Panes))
	activeCount := 0

	for _, pane := range capture.Panes {
		if seenIDs[pane.ID] {
			t.Fatalf("duplicate pane id %d in capture", pane.ID)
		}
		seenIDs[pane.ID] = true

		if seenNames[pane.Name] {
			t.Fatalf("duplicate pane name %q in capture", pane.Name)
		}
		seenNames[pane.Name] = true

		if pane.Active {
			activeCount++
		}

		if pane.Position == nil {
			t.Fatalf("pane %s is missing position in full-screen capture", pane.Name)
		}
		if pane.Position.Width <= 0 || pane.Position.Height <= 0 {
			t.Fatalf("pane %s has invalid position size %dx%d", pane.Name, pane.Position.Width, pane.Position.Height)
		}
	}

	if activeCount != 1 {
		t.Fatalf("capture must have exactly one active pane, got %d", activeCount)
	}
}
