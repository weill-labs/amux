package test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func captureJSONFor(tb testing.TB, runCmd func(...string) string) proto.CaptureJSON {
	tb.Helper()
	out := runCmd("capture", "--format", "json")
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		tb.Fatalf("captureJSON: %v\nraw: %s", err, out)
	}
	return capture
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

func jsonCaptureContainsPane(capture proto.CaptureJSON, name string) bool {
	for _, p := range capture.Panes {
		if p.Name == name {
			return true
		}
	}
	return false
}

func captureContainsAll(screen string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(screen, needle) {
			return false
		}
	}
	return true
}
