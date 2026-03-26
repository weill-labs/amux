package test

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// newAmuxHarnessWithConfig creates an AmuxHarness that launches the inner
// amux with a custom config file via the AMUX_CONFIG env var.
func newAmuxHarnessWithConfig(t *testing.T, configContent string) *AmuxHarness {
	t.Helper()
	outer := newServerHarness(t)

	// Write config to temp file
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	var b [4]byte
	rand.Read(b[:])
	inner := fmt.Sprintf("t-%x", b)

	h := &AmuxHarness{outer: outer, inner: inner, innerBin: amuxBin, tb: t, session: inner}

	// Launch inner amux with AMUX_CONFIG set
	outer.sendKeys("pane-1", fmt.Sprintf("AMUX_CONFIG=%s %s -s %s", configPath, amuxBin, inner), "Enter")
	outer.waitForTimeout("pane-1", "[pane-", "30s")

	t.Cleanup(func() {
		// Best-effort detach (only works with default prefix).
		exec.Command(amuxBin, "-s", inner, "list").Run()
		out, _ := exec.Command("pgrep", "-f", fmt.Sprintf("amux _server %s$", inner)).Output()
		for _, pid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if pid != "" {
				exec.Command("kill", pid).Run()
			}
		}
		time.Sleep(200 * time.Millisecond)
		socketDir := server.SocketDir()
		for _, suffix := range []string{"", ".log"} {
			exec.Command("rm", "-f", fmt.Sprintf("%s/%s%s", socketDir, inner, suffix)).Run()
		}
	})

	return h
}

func TestCustomPrefixKey(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
prefix = "C-b"
`)

	// Ctrl-b \ should split (using new prefix)
	gen := h.generation()
	h.sendKeys("C-b", "\\")
	h.waitLayout(gen)

	lines := h.captureAmuxContentLines()
	found := false
	for _, line := range lines {
		if strings.Contains(line, "[pane-1]") && strings.Contains(line, "[pane-2]") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Ctrl-b \\ should split with custom prefix\n%s", strings.Join(lines, "\n"))
	}
}

func TestTmuxPresetUsesTmuxBindings(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
preset = "tmux"
`)

	gen := h.generation()
	h.sendKeys("C-b", "%")
	h.waitLayout(gen)

	lines := h.captureAmuxContentLines()
	found := false
	for _, line := range lines {
		if strings.Contains(line, "[pane-1]") && strings.Contains(line, "[pane-2]") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Ctrl-b %% should split with tmux preset\n%s", strings.Join(lines, "\n"))
	}

	h.sendKeys("C-b", "q")
	if !h.waitFor("[2]", 3*time.Second) {
		t.Fatalf("Ctrl-b q should show pane labels with tmux preset, got:\n%s", h.captureOuter())
	}
}

func TestTmuxPresetReservesMarkPaneKey(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
preset = "tmux"
`)

	h.sendKeys("C-b", "m")
	h.sendKeys("e", "c", "h", "o", " ", "TMUX_M_OK", "Enter")

	if !h.waitFor("TMUX_M_OK", 3*time.Second) {
		t.Fatalf("expected TMUX_M_OK after tmux preset m test\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "mecho TMUX_M_OK") {
		t.Fatalf("Ctrl-b m should not leak literal input with tmux preset\nScreen:\n%s", screen)
	}
}

func TestCustomPrefixOldPrefixPassthrough(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
prefix = "C-b"
`)

	// Ctrl-a \ with old prefix should NOT split — server rejects
	// unrecognized prefix immediately, so assert without waiting.
	h.sendKeys("C-a", "\\")

	h.assertScreen("old prefix should not split", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && !strings.Contains(s, "[pane-2]")
	})
}

func TestCustomBindAddNewKey(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys.bind]
s = "split"
`)

	gen := h.generation()
	h.sendKeys("C-a", "s")
	h.waitLayout(gen)

	h.assertScreen("Ctrl-a s should split", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})

	// Default bindings should still work alongside custom ones
	gen = h.generation()
	h.sendKeys("C-a", "-")
	h.waitLayout(gen)

	h.assertScreen("default Ctrl-a - should still work", func(s string) bool {
		return strings.Contains(s, "[pane-3]")
	})
}

func TestCustomBindRemapKey(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys.bind]
o = "split v"
`)

	gen := h.generation()
	h.sendKeys("C-a", "o")
	h.waitLayout(gen)

	// Verify vertical split: panes on different X positions (left/right)
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position.X == p2.Position.X {
		t.Errorf("vertical split should put panes at different X, both at x=%d", p1.Position.X)
	}
}

func TestCustomUnbindKey(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
unbind = ["o"]
`)

	h.splitV()

	// pane-2 is active after split. Ctrl-a o should do nothing (unbound).
	// Server rejects unrecognized keys immediately, so assert without waiting.
	h.sendKeys("C-a", "o")

	h.assertActive("pane-2")
}

func TestCustomUnbindKeyShowsFeedback(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
unbind = ["o"]
`)

	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventPrefixMessageHidden+","+proto.UIEventPrefixMessageShown, "--client", "client-1")
	defer closer()
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageHidden {
		t.Fatalf("initial prefix-message state: got %q, want %q", ev.Type, proto.UIEventPrefixMessageHidden)
	}

	h.splitV()
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("expected post-split UI before testing unbound-key feedback, got:\n%s", h.captureOuter())
	}
	if out := h.runCmd("wait", "idle", "pane-2", "--timeout", "10s"); strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("expected split pane to go idle before unbound-key feedback test, got: %s\nouter:\n%s", strings.TrimSpace(out), h.captureOuter())
	}
	h.sendKeys("C-a", "o")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageShown {
		t.Fatalf("unbound-key event: got %q, want %q", ev.Type, proto.UIEventPrefixMessageShown)
	}

	if !h.waitFor("No binding for C-a o", 3*time.Second) {
		t.Fatalf("expected unbound-key feedback, got:\n%s", h.captureOuter())
	}
	h.assertActive("pane-2")
}

func TestUnsupportedPrefixKeyShowsFeedback(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	if out := h.runCmd("wait", "idle", "pane-1", "--timeout", "10s"); strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("expected inner pane to go idle before unsupported-key feedback test, got: %s\nouter:\n%s", strings.TrimSpace(out), h.captureOuter())
	}
	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventPrefixMessageHidden+","+proto.UIEventPrefixMessageShown, "--client", "client-1")
	defer closer()
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageHidden {
		t.Fatalf("initial prefix-message state: got %q, want %q", ev.Type, proto.UIEventPrefixMessageHidden)
	}
	h.sendKeys("C-a", "f")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageShown {
		t.Fatalf("unsupported-key event: got %q, want %q", ev.Type, proto.UIEventPrefixMessageShown)
	}

	if !h.waitFor("No binding for C-a f", 3*time.Second) {
		t.Fatalf("expected unsupported-key feedback, got:\n%s", h.captureOuter())
	}
	h.assertActive("pane-1")
}

func TestUnsupportedPrefixKeyFeedbackClearsOnLiteralPrefix(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)
	if out := h.runCmd("wait", "idle", "pane-1", "--timeout", "10s"); strings.Contains(out, "timeout") || strings.Contains(out, "not found") {
		t.Fatalf("expected inner pane to go idle before unsupported-key clear test, got: %s\nouter:\n%s", strings.TrimSpace(out), h.captureOuter())
	}
	scanner, closer := eventStream(t, h.session, "--filter", proto.UIEventPrefixMessageHidden+","+proto.UIEventPrefixMessageShown, "--client", "client-1")
	defer closer()
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageHidden {
		t.Fatalf("initial prefix-message state: got %q, want %q", ev.Type, proto.UIEventPrefixMessageHidden)
	}
	h.sendKeys("C-a", "f")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageShown {
		t.Fatalf("unsupported-key event: got %q, want %q", ev.Type, proto.UIEventPrefixMessageShown)
	}

	if !h.waitFor("No binding for C-a f", 3*time.Second) {
		t.Fatalf("expected unsupported-key feedback, got:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "C-a")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != proto.UIEventPrefixMessageHidden {
		t.Fatalf("literal-prefix clear event: got %q, want %q", ev.Type, proto.UIEventPrefixMessageHidden)
	}
	if !waitForOuterGone(h, "No binding for C-a f", 3*time.Second) {
		t.Fatalf("expected unsupported-key feedback to clear after literal prefix\nScreen:\n%s", h.captureOuter())
	}
}

func TestCustomDetachBinding(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
unbind = ["d"]

[keys.bind]
q = "detach"
`)

	// Ctrl-a q should detach the inner client. After detach, the outer
	// pane shows the shell prompt instead of amux chrome. Wait for the
	// global bar to disappear.
	h.sendKeys("C-a", "q")
	h.outer.waitFor("pane-1", "$")

	outerContent := h.captureOuter()
	if strings.Contains(outerContent, "amux") && strings.Contains(outerContent, "panes") {
		t.Errorf("inner amux should be detached (global bar still visible)\nOuter:\n%s", outerContent)
	}
}

func TestCustomDisplayPanesBinding(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
unbind = ["q"]

[keys.bind]
w = "display-panes"
`)

	gen := h.generation()
	h.sendKeys("C-a", "\\")
	h.waitLayout(gen)
	if !h.waitFor("[pane-2]", 3*time.Second) {
		t.Fatalf("expected inner client to render split before display-panes, got:\n%s", h.captureOuter())
	}

	h.sendKeys("C-a", "w")
	out := h.runCmd("wait", "ui", proto.UIEventDisplayPanesShown, "--timeout", "3s")
	if !strings.Contains(out, proto.UIEventDisplayPanesShown) {
		t.Fatalf("expected display-panes shown event, got: %s\nScreen:\n%s", out, h.captureOuter())
	}
	if !h.waitFor("[2]", 3*time.Second) {
		t.Fatalf("expected custom Ctrl-a w binding to show pane overlay, got:\n%s", h.captureOuter())
	}
}

func TestCustomChooseWindowBinding(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
[keys]
unbind = ["w"]

[keys.bind]
W = "choose-window"
`)

	h.runCmd("new-window", "--name", "logs")
	h.runCmd("select-window", "1")

	h.sendKeys("C-a", "W")
	if !h.waitFor("choose-window", 3*time.Second) {
		t.Fatalf("expected custom Ctrl-a W binding to show chooser, got:\n%s", h.captureOuter())
	}
}

func TestDefaultBindingsWithoutConfig(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, "")

	// Ctrl-a \ should split (default)
	gen := h.generation()
	h.sendKeys("C-a", "\\")
	h.waitLayout(gen)

	h.assertScreen("default split should work", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})

	// Ctrl-a o should cycle focus (default)
	gen = h.generation()
	h.sendKeys("C-a", "o")
	h.waitLayout(gen)

	h.assertActive("pane-1")
}
