package test

import (
	"strings"
	"testing"
)

func TestNestingEnvVarSet(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Verify AMUX_SESSION is set to the session name in pane shells
	h.sendKeys("pane-1", "echo AMUX_SESSION=$AMUX_SESSION", "Enter")
	h.waitFor("pane-1", "AMUX_SESSION="+h.session)

	// Verify AMUX_PANE contains the actual pane ID (not hardcoded "1")
	h.sendKeys("pane-1", "echo AMUX_PANE=$AMUX_PANE", "Enter")
	h.waitFor("pane-1", "AMUX_PANE=1") // pane-1 has ID 1

	// Spawn a second pane and verify it gets a different AMUX_PANE value
	h.splitH()
	h.sendKeys("pane-2", "echo AMUX_PANE=$AMUX_PANE", "Enter")
	h.waitFor("pane-2", "AMUX_PANE=2") // pane-2 has ID 2

	// Pane shells always identify themselves as amux.
	h.sendKeys("pane-1", "echo TERM=$TERM", "Enter")
	h.waitFor("pane-1", "TERM=amux")
}

func TestNestingSameSessionBlocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		suffix string // appended after "amux -s <session>"
	}{
		{"bare", ""},
		{"attach", " attach"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newServerHarness(t)
			h.sendKeys("pane-1", amuxBin+" -s "+h.session+tt.suffix, "Enter")
			h.waitFor("pane-1", "recursive nesting")
		})
	}
}

func TestNestingCrossSessionAllowed(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Running amux targeting a different session should not be blocked.
	// It will fail to connect (no server for "other-session"), but the
	// error should NOT be about nesting — it should be about connecting.
	h.sendKeys("pane-1", amuxBin+" -s other-session status 2>&1; echo DONE", "Enter")
	h.waitFor("pane-1", "DONE")

	// Capture pane output and verify it does NOT contain the nesting error
	out := h.runCmd("capture", "pane-1")
	if strings.Contains(out, "recursive nesting") {
		t.Errorf("cross-session should not be blocked, but got nesting error:\n%s", out)
	}
}

func TestNestingOverrideWithUnset(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Unsetting AMUX_SESSION should allow same-session invocation.
	// The command will fail to attach (server already running, client can't
	// start a second TUI), but the error should NOT be about nesting.
	h.sendKeys("pane-1", "unset AMUX_SESSION && "+amuxBin+" -s "+h.session+" status 2>&1; echo OVERRIDE_DONE", "Enter")
	h.waitFor("pane-1", "OVERRIDE_DONE")

	out := h.runCmd("capture", "pane-1")
	if strings.Contains(out, "recursive nesting") {
		t.Errorf("unset AMUX_SESSION should bypass nesting check, but got nesting error:\n%s", out)
	}
}
