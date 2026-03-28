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

	out := h.runCmd("wait", "ready", "pane-1", "--timeout", "5s")
	if strings.TrimSpace(out) != "ready" {
		t.Fatalf("wait-ready output = %q", out)
	}
}

func TestSendKeysWaitReadyAcceptsNonAgentPromptMarkers(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "export PS1='READY$ '", "Enter")
	h.waitIdle("pane-1")

	out := h.runCmd("send-keys", "pane-1", "--wait", "ready", "echo READY", "Enter")
	if strings.TrimSpace(out) != "Sent 11 bytes to pane-1" {
		t.Fatalf("send-keys --wait ready output = %q", out)
	}

	h.waitFor("pane-1", "READY")
	h.waitIdle("pane-1")
}

func TestWaitReadyRejectsRemovedContinueFlag(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	out := h.runCmd("wait", "ready", "pane-1", "--continue-known-dialogs")
	if strings.TrimSpace(out) != "wait ready: --continue-known-dialogs was removed; ready now waits for vt-idle + idle" {
		t.Fatalf("wait-ready removed-flag error = %q", out)
	}
}

func TestSendKeysWaitReadyRejectsRemovedContinueFlag(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	out := h.runCmd("send-keys", "pane-1", "--wait", "ready", "--continue-known-dialogs", "ship it")
	if strings.TrimSpace(out) != "send-keys: --continue-known-dialogs was removed; ready now waits for vt-idle + idle" {
		t.Fatalf("send-keys removed-flag error = %q", out)
	}
}

func TestWaitReadyRequiresIdleAfterVTOutputQuiesces(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "sh -c 'echo START; sleep 4'", "Enter")
	h.waitFor("pane-1", "START")

	out := h.runCmd("wait", "ready", "pane-1", "--timeout", "3s")
	if !strings.Contains(out, "timeout waiting for pane-1 to become ready") {
		t.Fatalf("wait-ready timeout output = %q", out)
	}

	h.waitIdle("pane-1")
}
