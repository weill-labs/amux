package test

import (
	"strings"
	"testing"
)

func TestWaitVTIdle_EventBased(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	h.sendKeys("pane-1", "printf 'one'; sleep 0.05; printf 'two'; echo done", "Enter")

	out := h.runCmd("wait", "vt-idle", "pane-1", "--settle", "30ms", "--timeout", "2s")
	if strings.Contains(out, "timeout") || strings.Contains(out, "unknown command") {
		t.Fatalf("wait-vt-idle failed: %s", out)
	}

	h.waitFor("pane-1", "done")
}
