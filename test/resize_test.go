package test

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTerminalResize(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.splitV()

	// Resize the tmux pane (simulates terminal resize -> SIGWINCH)
	exec.Command("tmux", "resize-pane", "-t", h.session, "-x", "120", "-y", "40").Run()
	time.Sleep(1 * time.Second)

	h.assertScreen("both panes visible after resize", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	col := h.verticalBorderCol()
	if col < 0 {
		t.Fatal("vertical border missing after resize")
	}

	if col < 40 || col > 80 {
		t.Errorf("border at col %d, expected near middle of 120-wide terminal", col)
	}
}
