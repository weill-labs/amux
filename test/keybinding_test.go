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

func writeTestConfigFile(t *testing.T, configContent string) string {
	t.Helper()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return configPath
}

func randomTestSessionName(t *testing.T) string {
	t.Helper()

	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return fmt.Sprintf("t-%x", b)
}

// newAmuxHarnessWithConfig creates an AmuxHarness that launches the inner
// amux with a custom config file via the AMUX_CONFIG env var.
func newAmuxHarnessWithConfig(t *testing.T, configContent string) *AmuxHarness {
	t.Helper()
	outer := newServerHarness(t)
	configPath := writeTestConfigFile(t, configContent)
	inner := randomTestSessionName(t)

	h := &AmuxHarness{outer: outer, inner: inner, innerBin: amuxBin, tb: t, session: inner}

	// Launch inner amux with AMUX_CONFIG set.
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

func TestDefaultBindingsWithoutConfig(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, "")

	// Ctrl-a \ should split (default).
	gen := h.generation()
	h.sendKeys("C-a", "\\")
	h.waitLayout(gen)

	h.assertScreen("default split should work", func(s string) bool {
		return strings.Contains(s, "[pane-2]")
	})
	h.assertActive("pane-1")

	// Ctrl-a o should cycle focus (default).
	gen = h.generation()
	h.sendKeys("C-a", "o")
	h.waitLayout(gen)

	h.assertActive("pane-2")

	// Ctrl-a a should add a pane without stealing focus.
	gen = h.generation()
	h.sendKeys("C-a", "a")
	h.waitLayout(gen)

	h.assertActive("pane-2")
	h.assertScreen("default add-pane should work", func(s string) bool {
		return strings.Contains(s, "[pane-3]")
	})
}

func TestRootHorizontalBindingWhileLeadFocused(t *testing.T) {
	t.Parallel()

	h := newAmuxHarness(t)

	h.runCmd("split", "pane-1", "v")
	h.runCmd("set-lead", "pane-1")
	h.runCmd("focus", "pane-2")
	h.runCmd("split", "pane-2", "--horizontal")
	h.runCmd("focus", "pane-1")

	gen := h.generation()
	h.sendKeys("C-a", "_")
	h.waitLayout(gen)

	frame := extractFrame(h.capture(), h.session)
	assertGolden(t, "root_horizontal_binding_lead_focused.golden", frame)

	out := h.runCmd("status")
	if !strings.Contains(out, "panes: 4 total") {
		t.Fatalf("expected 4 panes after root horizontal split on focused lead, got: %s", out)
	}
}
