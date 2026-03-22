package test

import (
	"strings"
	"testing"
)

func TestBroadcastTargetsAndAtomicity(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitRootH()

	panesMarker := "BROADCAST_PANES_MARKER"
	out := h.runCmd("broadcast", "--panes", "pane-1,pane-3", "echo "+panesMarker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --panes failed: %s", out)
	}

	h.waitFor("pane-1", panesMarker)
	h.waitFor("pane-3", panesMarker)

	if pane2 := h.runCmd("capture", "pane-2"); strings.Contains(pane2, panesMarker) {
		t.Fatalf("pane-2 should not receive broadcast marker, got:\n%s", pane2)
	}

	atomicMarker := "BROADCAST_ATOMIC_MARKER"
	out = h.runCmd("broadcast", "--panes", "pane-1,missing-pane", "echo "+atomicMarker, "Enter")
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not found error, got: %s", out)
	}

	if pane1 := h.runCmd("capture", "pane-1"); strings.Contains(pane1, atomicMarker) {
		t.Fatalf("pane-1 should not receive atomic-failure broadcast marker, got:\n%s", pane1)
	}

	dedupeMarker := "585987"
	out = h.runCmd("broadcast", "--panes", "pane-1,pane-1,pane-2", "echo $((314159+271828))", "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast dedupe failed: %s", out)
	}

	h.waitFor("pane-1", dedupeMarker)
	h.waitFor("pane-2", dedupeMarker)

	if pane1 := h.runCmd("capture", "pane-1"); strings.Count(pane1, dedupeMarker) != 1 {
		t.Fatalf("pane-1 should see the deduped result exactly once, got:\n%s", pane1)
	}

	matchMarker := "BROADCAST_MATCH_MARKER"
	out = h.runCmd("broadcast", "--match", "pane-[12]", "echo "+matchMarker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --match failed: %s", out)
	}

	h.waitFor("pane-1", matchMarker)
	h.waitFor("pane-2", matchMarker)

	if pane3 := h.runCmd("capture", "pane-3"); strings.Contains(pane3, matchMarker) {
		t.Fatalf("pane-3 should not receive match broadcast marker, got:\n%s", pane3)
	}

	h.runCmd("new-window", "--name", "logs")
	h.doSplit("--name", "logs-worker")
	h.runCmd("select-window", "1")

	windowMarker := "BROADCAST_WINDOW_MARKER"
	out = h.runCmd("broadcast", "--window", "logs", "echo "+windowMarker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --window failed: %s", out)
	}

	h.waitFor("pane-4", windowMarker)
	h.waitFor("logs-worker", windowMarker)

	if pane1 := h.runCmd("capture", "pane-1"); strings.Contains(pane1, windowMarker) {
		t.Fatalf("pane-1 should not receive window broadcast marker, got:\n%s", pane1)
	}
	if pane2 := h.runCmd("capture", "pane-2"); strings.Contains(pane2, windowMarker) {
		t.Fatalf("pane-2 should not receive window broadcast marker, got:\n%s", pane2)
	}
	if pane3 := h.runCmd("capture", "pane-3"); strings.Contains(pane3, windowMarker) {
		t.Fatalf("pane-3 should not receive window broadcast marker, got:\n%s", pane3)
	}
}
