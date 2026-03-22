package test

import (
	"strings"
	"testing"
)

func TestBroadcastPanes(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitRootH()

	marker := "BROADCAST_PANES_MARKER"
	out := h.runCmd("broadcast", "--panes", "pane-1,pane-3", "echo "+marker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --panes failed: %s", out)
	}

	h.waitFor("pane-1", marker)
	h.waitFor("pane-3", marker)

	if pane2 := h.runCmd("capture", "pane-2"); strings.Contains(pane2, marker) {
		t.Fatalf("pane-2 should not receive broadcast marker, got:\n%s", pane2)
	}
}

func TestBroadcastWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.runCmd("new-window", "--name", "logs")
	h.doSplit("--name", "logs-worker")
	h.runCmd("select-window", "1")

	marker := "BROADCAST_WINDOW_MARKER"
	out := h.runCmd("broadcast", "--window", "logs", "echo "+marker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --window failed: %s", out)
	}

	h.waitFor("pane-3", marker)
	h.waitFor("logs-worker", marker)

	if pane1 := h.runCmd("capture", "pane-1"); strings.Contains(pane1, marker) {
		t.Fatalf("pane-1 should not receive window broadcast marker, got:\n%s", pane1)
	}
	if pane2 := h.runCmd("capture", "pane-2"); strings.Contains(pane2, marker) {
		t.Fatalf("pane-2 should not receive window broadcast marker, got:\n%s", pane2)
	}
}

func TestBroadcastMatch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.doSplit("v", "--name", "worker-alpha")
	h.doSplit("root", "--name", "worker-beta")

	marker := "BROADCAST_MATCH_MARKER"
	out := h.runCmd("broadcast", "--match", "worker-*", "echo "+marker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast --match failed: %s", out)
	}

	h.waitFor("worker-alpha", marker)
	h.waitFor("worker-beta", marker)

	if pane1 := h.runCmd("capture", "pane-1"); strings.Contains(pane1, marker) {
		t.Fatalf("pane-1 should not receive match broadcast marker, got:\n%s", pane1)
	}
}

func TestBroadcastInvalidPaneIsAtomic(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	marker := "BROADCAST_ATOMIC_MARKER"
	out := h.runCmd("broadcast", "--panes", "pane-1,missing-pane", "echo "+marker, "Enter")
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected not found error, got: %s", out)
	}

	if pane1 := h.runCmd("capture", "pane-1"); strings.Contains(pane1, marker) {
		t.Fatalf("pane-1 should not receive atomic-failure broadcast marker, got:\n%s", pane1)
	}
	if pane2 := h.runCmd("capture", "pane-2"); strings.Contains(pane2, marker) {
		t.Fatalf("pane-2 should not receive atomic-failure broadcast marker, got:\n%s", pane2)
	}
}

func TestBroadcastDedupesPaneRefs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	marker := "BROADCAST_DEDUPE_MARKER"
	out := h.runCmd("broadcast", "--panes", "pane-1,pane-1,pane-2", "echo "+marker, "Enter")
	if strings.Contains(out, "unknown command") || strings.Contains(out, "usage:") {
		t.Fatalf("broadcast dedupe failed: %s", out)
	}

	h.waitFor("pane-1", marker)
	h.waitFor("pane-2", marker)

	if pane1 := h.runCmd("capture", "pane-1"); strings.Count(pane1, marker) != 2 {
		t.Fatalf("pane-1 should see exactly one echoed command and one output line, got:\n%s", pane1)
	}
}

func TestBroadcastUsageAndMatchErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		args   []string
		errSub string
	}{
		{
			name:   "combined selectors",
			args:   []string{"--panes", "pane-1", "--window", "1", "echo bad", "Enter"},
			errSub: "specify exactly one",
		},
		{
			name:   "empty match",
			args:   []string{"--match", "missing-*", "echo bad", "Enter"},
			errSub: "no panes match",
		},
		{
			name:   "missing keys",
			args:   []string{"--window", "1"},
			errSub: "usage: broadcast",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newServerHarness(t)
			out := h.runCmd(append([]string{"broadcast"}, tt.args...)...)
			if !strings.Contains(out, tt.errSub) {
				t.Fatalf("broadcast output missing %q:\n%s", tt.errSub, out)
			}
		})
	}
}
