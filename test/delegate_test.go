package test

import (
	"strings"
	"testing"
	"time"
)

func TestSendKeysWaitInputIdleTargetsExplicitPaneWithoutChangingFocus(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	h.runCmd("focus", "pane-1")

	before := h.activePaneName()
	out := h.runCmd("send-keys", "pane-2", "--wait", "ui=input-idle", "echo SEND_WAIT_IDLE_OK", "Enter")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("send-keys --wait ui=input-idle failed: %s", out)
	}

	waitOut := h.runCmd("wait", "content", "pane-2", "SEND_WAIT_IDLE_OK", "--timeout", "3s")
	if strings.Contains(waitOut, "timeout") {
		t.Fatalf("expected SEND_WAIT_IDLE_OK in pane-2, got: %s\nscreen:\n%s", waitOut, h.captureOuter())
	}
	if got := h.activePaneName(); got != before {
		t.Fatalf("active pane changed after targeted send-keys: got %s want %s", got, before)
	}
}

func TestTypeKeysTargetsExplicitPaneWithoutChangingFocus(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	h.runCmd("focus", "pane-1")

	before := h.activePaneName()
	out := h.runCmd("type-keys", "pane-2", "echo TYPE_TARGET_OK", "Enter")
	if strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("type-keys target pane failed: %s", out)
	}

	waitOut := h.runCmd("wait", "content", "pane-2", "TYPE_TARGET_OK", "--timeout", "3s")
	if strings.Contains(waitOut, "timeout") {
		t.Fatalf("expected TYPE_TARGET_OK in pane-2, got: %s\nscreen:\n%s", waitOut, h.captureOuter())
	}
	if got := h.activePaneName(); got != before {
		t.Fatalf("active pane changed after targeted type-keys: got %s want %s", got, before)
	}
}

func TestDelegateSendsTextWaitsAndConfirmsBusy(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.splitV()
	h.runCmd("focus", "pane-1")

	before := h.activePaneName()
	out := h.runCmd("delegate", "pane-2", "--start-timeout", "3s", "sleep 1; echo DELEGATE_DONE")
	if !strings.Contains(out, "Delegated ") {
		t.Fatalf("delegate output = %q", out)
	}
	if got := h.activePaneName(); got != before {
		t.Fatalf("active pane changed after delegate: got %s want %s", got, before)
	}

	waitOut := h.runCmd("wait", "content", "pane-2", "DELEGATE_DONE", "--timeout", "4s")
	if strings.Contains(waitOut, "timeout") {
		t.Fatalf("expected DELEGATE_DONE in pane-2, got: %s\nscreen:\n%s", waitOut, h.captureOuter())
	}
}

func TestDelegateFailsWhenPaneNeverStarts(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	out := h.runCmd("delegate", "pane-1", "--start-timeout", "100ms", "true")
	if !strings.Contains(out, "timeout waiting for pane-1 to become busy") {
		t.Fatalf("delegate timeout output = %q", out)
	}
}

func TestTypeKeysSinglePaneLikeArgStillTypesLiteral(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.runCmd("type-keys", "echo pane-1", "Enter")
	if !h.waitFor("pane-1", 3*time.Second) {
		t.Fatalf("expected literal pane-like text to be typed\nscreen:\n%s", h.captureOuter())
	}
}
