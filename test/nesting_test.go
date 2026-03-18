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

	// Verify AMUX_PANE is still set (regression check)
	h.sendKeys("pane-1", "echo AMUX_PANE=$AMUX_PANE", "Enter")
	h.waitFor("pane-1", "AMUX_PANE=1")
}

func TestNestingSameSessionBlocked(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Running amux targeting the same session inside a pane should fail
	h.sendKeys("pane-1", amuxBin+" -s "+h.session, "Enter")
	h.waitFor("pane-1", "cannot attach to session")
}

func TestNestingSameSessionBlockedAttach(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// The "attach" subcommand should also be blocked
	h.sendKeys("pane-1", amuxBin+" -s "+h.session+" attach", "Enter")
	h.waitFor("pane-1", "cannot attach to session")
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
