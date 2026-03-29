package test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

const (
	queryRetryTimeout = 5 * time.Second
	queryRetryDelay   = 25 * time.Millisecond
)

func captureJSONFor(tb testing.TB, runCmd func(...string) string) proto.CaptureJSON {
	tb.Helper()
	return captureJSONWithArgsFor(tb, runCmd, "capture", "--format", "json")
}

func captureJSONWithArgsFor(tb testing.TB, runCmd func(...string) string, args ...string) proto.CaptureJSON {
	tb.Helper()

	deadline := time.Now().Add(queryRetryTimeout)
	for {
		raw := runCmd(args...)
		var capture proto.CaptureJSON
		if err := json.Unmarshal([]byte(raw), &capture); err == nil {
			return capture
		} else if time.Now().After(deadline) || !(isCaptureUnavailable(raw) || isTransientSessionQueryFailure(raw)) {
			tb.Fatalf("captureJSON(%v): %v\nraw: %s", args, err, raw)
		}
		time.Sleep(queryRetryDelay)
	}
}

func capturePaneJSONFor(tb testing.TB, pane string, runCmd func(...string) string) proto.CapturePane {
	tb.Helper()

	deadline := time.Now().Add(queryRetryTimeout)
	for {
		raw := runCmd("capture", "--format", "json", pane)
		var capture proto.CapturePane
		if err := json.Unmarshal([]byte(raw), &capture); err == nil {
			return capture
		} else if time.Now().After(deadline) || !(isCaptureUnavailable(raw) || isTransientSessionQueryFailure(raw)) {
			tb.Fatalf("capturePaneJSON(%s): %v\nraw: %s", pane, err, raw)
		}
		time.Sleep(queryRetryDelay)
	}
}

func isTransientSessionQueryFailure(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	return strings.Contains(raw, "server not running") ||
		strings.Contains(raw, "session shutting down") ||
		strings.Contains(raw, "EOF")
}

func stopLongRunningCommand(tb testing.TB, h *ServerHarness, pane string) {
	tb.Helper()

	h.sendKeys(pane, "C-c")

	deadline := time.Now().Add(queryRetryTimeout)
	for {
		out := h.runCmd("wait", "busy", pane, "--timeout", "100ms")
		switch {
		case isTransientSessionQueryFailure(out):
			return
		case strings.Contains(out, "timeout") || strings.Contains(out, "not found"):
			return
		case time.Now().After(deadline):
			tb.Fatalf("stopLongRunningCommand(%s): %s", pane, strings.TrimSpace(out))
		}
		time.Sleep(queryRetryDelay)
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
