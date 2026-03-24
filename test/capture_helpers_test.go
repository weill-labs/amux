package test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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

const (
	captureRetryTimeout = 5 * time.Second
	captureRetryDelay   = 25 * time.Millisecond
)

func captureJSONRetrying(tb testing.TB, runCmd func(...string) string) proto.CaptureJSON {
	tb.Helper()

	deadline := time.Now().Add(captureRetryTimeout)
	var last string
	for {
		last = runCmd("capture", "--format", "json")
		var capture proto.CaptureJSON
		if err := json.Unmarshal([]byte(last), &capture); err == nil {
			return capture
		} else if time.Now().After(deadline) || !isTransientCaptureFailure(last) {
			tb.Fatalf("captureJSON: %v\nraw: %s", err, last)
		}
		time.Sleep(captureRetryDelay)
	}
}

func capturePaneJSONRetrying(tb testing.TB, pane string, runCmd func(...string) string) proto.CapturePane {
	tb.Helper()

	deadline := time.Now().Add(captureRetryTimeout)
	var last string
	for {
		last = runCmd("capture", "--format", "json", pane)
		var capture proto.CapturePane
		if err := json.Unmarshal([]byte(last), &capture); err == nil {
			return capture
		} else if time.Now().After(deadline) || !isTransientCaptureFailure(last) {
			tb.Fatalf("capturePaneJSON(%s): %v\nraw: %s", pane, err, last)
		}
		time.Sleep(captureRetryDelay)
	}
}

func isTransientCaptureFailure(raw string) bool {
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

	deadline := time.Now().Add(captureRetryTimeout)
	for {
		out := h.runCmd("wait-idle", pane, "--timeout", "1s")
		switch {
		case strings.Contains(out, "server not running"), strings.Contains(out, "session shutting down"):
			return
		case !strings.Contains(out, "timeout") && !strings.Contains(out, "not found"):
			return
		case time.Now().After(deadline):
			tb.Fatalf("stopLongRunningCommand(%s): %s", pane, strings.TrimSpace(out))
		}
		time.Sleep(captureRetryDelay)
	}
}

func newPersistentHarnessWithCleanShutdown(tb testing.TB) *ServerHarness {
	tb.Helper()

	h := newServerHarnessPersistent(tb)
	tb.Cleanup(func() {
		shutdownServerHarness(tb, h)
	})
	return h
}

func shutdownServerHarness(tb testing.TB, h *ServerHarness) {
	tb.Helper()

	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	if h.client != nil {
		h.client.close()
		h.client = nil
	}
	if err := h.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		tb.Fatalf("stopping server: %v", err)
	}
	if h.shutdownPipe != nil {
		h.waitForShutdownSignal(5 * time.Second)
	}
	done := make(chan struct{})
	go func() {
		_ = h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = h.cmd.Process.Kill()
		tb.Fatal("server did not shut down within 5s")
	}
	h.cmd = nil
}

func shutdownSinglePaneSession(tb testing.TB, h *ServerHarness) {
	tb.Helper()

	if h == nil || h.cmd == nil {
		return
	}
	out := h.runCmd("kill", "--cleanup", "--timeout", "2s", "pane-1")
	if !isTransientCaptureFailure(out) &&
		!strings.Contains(out, "session exiting") &&
		!strings.Contains(out, "Cleaning up pane-1") &&
		!strings.Contains(out, "Killed pane-1") {
		tb.Fatalf("kill pane-1: %s", strings.TrimSpace(out))
	}
	if h.shutdownPipe != nil {
		h.waitForShutdownSignal(5 * time.Second)
	}
	done := make(chan struct{})
	go func() {
		_ = h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = h.cmd.Process.Kill()
		tb.Fatal("server did not shut down within 5s")
	}
	h.client = nil
	h.cmd = nil
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
