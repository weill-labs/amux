package proto

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPaneMetaJSONRoundTripPreservesTrackedRefs(t *testing.T) {
	t.Parallel()

	want := PaneMeta{
		Name:      "pane-7",
		Host:      "gpu-box",
		Task:      "LAB-572",
		Color:     "f38ba8",
		GitBranch: "lab-572-move-pane-meta-proto",
		PR:        "123",
		KV: map[string]string{
			"branch": "lab-572-move-pane-meta-proto",
		},
		TrackedPRs: []TrackedPR{{
			Number: 123,
			Status: TrackedStatusActive,
		}},
		TrackedIssues: []TrackedIssue{{
			ID:     "LAB-572",
			Status: TrackedStatusActive,
		}},
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal(PaneMeta): %v", err)
	}

	var got PaneMeta
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal(PaneMeta): %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PaneMeta round trip = %+v, want %+v", got, want)
	}
}

func TestCaptureSnapshotCarriesTerminalMetadata(t *testing.T) {
	t.Parallel()

	snap := CaptureSnapshot{
		LiveHistory: []CaptureHistoryLine{{
			Text:        "wrapped history",
			SourceWidth: 72,
			Filled:      true,
		}},
		ContentRows: []CaptureHistoryLine{{
			Text:        "visible row",
			SourceWidth: 80,
		}},
		Terminal: TerminalState{
			AltScreen: true,
			Mouse: MouseProtocol{
				Tracking: MouseTrackingButton,
				SGR:      true,
			},
			CursorStyle:    "block",
			CursorBlinking: true,
		},
	}

	if !snap.Terminal.Mouse.Enabled() {
		t.Fatal("CaptureSnapshot should retain enabled mouse protocol state")
	}
	if got, want := snap.Terminal.Mouse.TrackingName(), "button"; got != want {
		t.Fatalf("TrackingName() = %q, want %q", got, want)
	}
	if got, want := snap.Terminal.CursorStyle, "block"; got != want {
		t.Fatalf("CursorStyle = %q, want %q", got, want)
	}
}

func TestDefaultScrollbackLinesIsPositive(t *testing.T) {
	t.Parallel()

	if DefaultScrollbackLines <= 0 {
		t.Fatalf("DefaultScrollbackLines = %d, want > 0", DefaultScrollbackLines)
	}
}
