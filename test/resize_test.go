package test

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalResize(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	// Resize the outer server's window, which resizes the outer pane's PTY,
	// sending SIGWINCH to the inner amux client, which forwards resize to
	// the inner server.
	h.outer.runCmd("resize-window", "120", "40")

	// Wait for the inner server to process the resize — the inner client
	// receives SIGWINCH and sends MsgTypeResize to the inner server.
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes not visible after resize\nScreen:\n%s", screen)
	}

	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("vertical border missing after resize")
	}

	if col < 40 || col > 80 {
		t.Errorf("border at col %d, expected near middle of 120-wide terminal", col)
	}
}
