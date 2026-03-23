package test

import (
	"strings"
	"testing"
	"time"
)

func TestPaneLogCLI(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	// The harness starts with one pane; pane-log should show its creation.
	out := h.runCmd("pane-log")
	for _, want := range []string{
		"TS",
		"EVENT",
		"ID",
		"PANE",
		"HOST",
		"REASON",
		"create",
		"pane-1",
		"local",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pane-log missing %q:\n%s", want, out)
		}
	}
}

func TestPaneLogShowsExitReason(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	gen := h.generation()
	h.splitH()
	h.waitLayout(gen)

	// Wait for the shell in pane-2 to start before sending exit.
	h.waitForPaneContent("pane-2", "$", 5*time.Second)

	gen = h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen)

	out := h.runCmd("pane-log")
	if !strings.Contains(out, "exit") {
		t.Fatalf("pane-log should contain exit event:\n%s", out)
	}
	if !strings.Contains(out, "pane-2") {
		t.Fatalf("pane-log should mention pane-2:\n%s", out)
	}
}
