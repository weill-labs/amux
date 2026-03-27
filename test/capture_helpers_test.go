package test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func captureJSONFor(tb testing.TB, runCmd func(...string) string) proto.CaptureJSON {
	tb.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var last string
	for {
		last = runCmd("capture", "--format", "json")
		var capture proto.CaptureJSON
		if err := json.Unmarshal([]byte(last), &capture); err == nil {
			return capture
		} else if !isCaptureUnavailable(last) || !time.Now().Before(deadline) {
			tb.Fatalf("captureJSON: %v\nraw: %s", err, last)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func jsonPaneFor(tb testing.TB, capture proto.CaptureJSON, name string) proto.CapturePane {
	tb.Helper()
	for _, p := range capture.Panes {
		if p.Name == name {
			if p.Position == nil {
				tb.Fatalf("pane %q has nil Position in full-screen capture", name)
			}
			return p
		}
	}
	tb.Fatalf("pane %q not found in JSON capture", name)
	return proto.CapturePane{}
}

func assertJSONPaneColumnIndex(tb testing.TB, capture proto.CaptureJSON, name string, want int) {
	tb.Helper()
	if got := jsonPaneFor(tb, capture, name).ColumnIndex; got != want {
		tb.Fatalf("%s column_index = %d, want %d", name, got, want)
	}
}

func activePaneNameFor(tb testing.TB, capture proto.CaptureJSON) string {
	tb.Helper()
	for _, p := range capture.Panes {
		if p.Active {
			return p.Name
		}
	}
	tb.Fatal("no active pane found")
	return ""
}

func globalBarFromLines(lines []string) string {
	for _, line := range lines {
		if isGlobalBar(line) {
			return line
		}
	}
	return ""
}
