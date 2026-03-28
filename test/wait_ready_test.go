package test

import (
	"strings"
	"testing"
)

func TestWaitReadyAcceptsNonAgentPromptMarkers(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "export PS1='READY$ '", "Enter")
	h.waitIdle("pane-1")

	out := h.runCmd("wait", "idle", "pane-1", "--timeout", "5s")
	if strings.TrimSpace(out) != "idle" {
		t.Fatalf("wait-idle output = %q", out)
	}
}

func TestSendKeysWaitReadyAcceptsNonAgentPromptMarkers(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "export PS1='READY$ '", "Enter")
	h.waitIdle("pane-1")

	out := h.runCmd("send-keys", "pane-1", "--wait", "idle", "echo READY", "Enter")
	if strings.TrimSpace(out) != "Sent 11 bytes to pane-1" {
		t.Fatalf("send-keys --wait idle output = %q", out)
	}

	h.waitFor("pane-1", "READY")
	h.waitIdle("pane-1")
}

func TestWaitReadyIsRemoved(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	out := h.runCmd("wait", "ready", "pane-1")
	if strings.TrimSpace(out) != "amux wait: unknown wait kind: ready" {
		t.Fatalf("wait-ready removal error = %q", out)
	}
}

func TestSendKeysWaitReadyIsRemoved(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	out := h.runCmd("send-keys", "pane-1", "--wait", "ready", "ship it")
	if strings.TrimSpace(out) != `amux send-keys: send-keys: unsupported --wait target "ready" (want idle or ui=input-idle)` {
		t.Fatalf("send-keys wait-ready removal error = %q", out)
	}
}

func TestWaitIdleReturnsWhenOutputQuiescesEvenIfChildStillRuns(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "sh -c 'echo START; sleep 4'", "Enter")
	h.waitFor("pane-1", "START")

	out := h.runCmd("wait", "idle", "pane-1", "--timeout", "5s")
	if strings.TrimSpace(out) != "idle" {
		t.Fatalf("wait-idle output = %q", out)
	}

	stopLongRunningCommand(t, h, "pane-1")
}
