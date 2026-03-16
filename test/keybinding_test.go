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

	h := &AmuxHarness{outer: outer, inner: inner, tb: t, session: inner}

	// Launch inner amux with AMUX_CONFIG set
	outer.sendKeys("pane-1", fmt.Sprintf("AMUX_CONFIG=%s %s -s %s", configPath, amuxBin, inner), "Enter")
	outer.waitFor("pane-1", "[pane-")

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

	// Verify horizontal split: panes on different Y positions
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position.Y == p2.Position.Y {
		t.Errorf("horizontal split should put panes at different Y, both at y=%d", p1.Position.Y)
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
