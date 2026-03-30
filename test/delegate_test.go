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

func TestTypeKeysSinglePaneLikeArgStillTypesLiteral(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	h.sendClientKeys("echo pane-1", "Enter")
	if !h.waitFor("pane-1", 3*time.Second) {
		t.Fatalf("expected literal pane-like text to be typed\nscreen:\n%s", h.captureOuter())
	}
}
