package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Phase 1: Capture format coverage
// ---------------------------------------------------------------------------

func TestStressCaptureFormats(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Plain text capture (full screen)
	plain := h.capture()
	if !strings.Contains(plain, "[pane-1]") || !strings.Contains(plain, "[pane-2]") {
		t.Errorf("plain capture should show both panes, got:\n%s", plain)
	}

	// JSON capture
	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Fatalf("JSON capture should have 2 panes, got %d", len(c.Panes))
	}
	if c.Session == "" {
		t.Error("JSON capture should include session name")
	}
	if c.Width == 0 || c.Height == 0 {
		t.Errorf("JSON capture should have dimensions, got %dx%d", c.Width, c.Height)
	}
	for _, p := range c.Panes {
		if p.Position == nil {
			t.Errorf("pane %s should have position in full-screen capture", p.Name)
		}
		if p.Name == "" {
			t.Error("pane should have a name")
		}
	}

	// ANSI capture
	ansi := h.runCmd("capture", "--ansi")
	if !strings.Contains(ansi, "\x1b[") {
		t.Error("ANSI capture should contain escape sequences")
	}

	// Color map capture
	colors := h.runCmd("capture", "--colors")
	if colors == "" {
		t.Error("color capture should not be empty")
	}

	// Single-pane capture
	p1 := h.runCmd("capture", "pane-1")
	if strings.Contains(p1, "[pane-2]") {
		t.Errorf("single-pane capture should only show pane-1 content, got:\n%s", p1)
	}

	// Single-pane ANSI capture — write colored text so escapes are present.
	// Use a split marker (COL + DONE) so waitFor matches the output, not the command.
	h.sendKeys("pane-1", `printf '\033[31mRED\033[m\n' && printf COL; printf 'DONE\n'`, "Enter")
	h.waitFor("pane-1", "COLDONE")
	p1ansi := h.runCmd("capture", "--ansi", "pane-1")
	if !strings.Contains(p1ansi, "\x1b[") {
		t.Errorf("single-pane ANSI capture should contain escape sequences, got:\n%s", p1ansi)
	}
}

// ---------------------------------------------------------------------------
// Phase 2: Rapid operations
// ---------------------------------------------------------------------------

func TestStressRapidSplits(t *testing.T) {
	t.Parallel()
	h := newServerHarnessWithSize(t, 200, 60)

	// 7 consecutive vertical splits → 8 panes side-by-side (at 200 cols, ~24 cols each)
	for i := 0; i < 7; i++ {
		h.splitV()
	}

	c := h.captureJSON()
	if len(c.Panes) != 8 {
		t.Fatalf("expected 8 panes after 7 vsplits, got %d", len(c.Panes))
	}

	// All panes should have non-zero width and be ordered by X
	for i := 0; i < len(c.Panes)-1; i++ {
		if c.Panes[i].Position.Width <= 0 {
			t.Errorf("%s has zero width", c.Panes[i].Name)
		}
		if c.Panes[i].Position.X >= c.Panes[i+1].Position.X {
			t.Errorf("%s (x=%d) should be left of %s (x=%d)",
				c.Panes[i].Name, c.Panes[i].Position.X,
				c.Panes[i+1].Name, c.Panes[i+1].Position.X)
		}
	}

	// All panes should be renderable with status lines
	screen := h.capture()
	for i := 1; i <= 8; i++ {
		label := fmt.Sprintf("[pane-%d]", i)
		if !strings.Contains(screen, label) {
			t.Errorf("pane-%d status line not found in capture", i)
		}
	}
}

func TestStressRapidZoomToggle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	// Toggle zoom 10 times
	for i := 0; i < 10; i++ {
		h.runCmd("zoom", "pane-1")
	}

	// After 10 toggles (even count), should be unzoomed
	status := h.runCmd("status")
	if strings.Contains(status, "zoomed") {
		t.Errorf("after 10 zoom toggles (even), should be unzoomed, got:\n%s", status)
	}

	h.assertScreen("both panes visible after even toggles", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	})

	// One more toggle → zoomed (odd count total)
	h.runCmd("zoom", "pane-1")
	status = h.runCmd("status")
	if !strings.Contains(status, "zoomed") {
		t.Errorf("after 11 zoom toggles (odd), should be zoomed, got:\n%s", status)
	}
}

// ---------------------------------------------------------------------------
// Phase 3: Kill edge cases
// ---------------------------------------------------------------------------

func TestStressKillLastPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("kill", "pane-1")
	if !strings.Contains(out, "cannot") {
		t.Errorf("killing last pane should fail with 'cannot', got:\n%s", out)
	}

	// Server should still be running
	c := h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("should still have 1 pane, got %d", len(c.Panes))
	}
}

func TestStressKillAllButLast(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitV()
	h.splitV()

	// Kill panes from right to left
	for _, name := range []string{"pane-4", "pane-3", "pane-2"} {
		out := h.runCmd("kill", name)
		if !strings.Contains(out, "Killed") {
			t.Fatalf("kill %s should succeed, got:\n%s", name, out)
		}
	}

	c := h.captureJSON()
	if len(c.Panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(c.Panes))
	}
	h.jsonPane(c, "pane-1")
}

// ---------------------------------------------------------------------------
// Phase 4: Swap edge cases
// ---------------------------------------------------------------------------

func TestStressSwapSelf(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Swap pane with itself — should be a graceful no-op or error
	out := h.runCmd("swap", "pane-1", "pane-1")

	// Verify no crash — pane positions should be unchanged
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position.X >= p2.Position.X {
		t.Errorf("swap self should not break layout: p1.x=%d, p2.x=%d\nswap output: %s",
			p1.Position.X, p2.Position.X, out)
	}
}

func TestStressRotateReverse(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()
	h.splitV()

	// Record initial positions
	c := h.captureJSON()
	initial := make(map[string]int)
	for _, p := range c.Panes {
		initial[p.Name] = p.Position.X
	}

	// Rotate forward then backward → positions should return to original
	h.runCmd("rotate")
	out := h.runCmd("rotate", "--reverse")
	if strings.Contains(out, "unknown") || strings.Contains(out, "error") {
		t.Fatalf("rotate --reverse failed: %s", out)
	}

	c = h.captureJSON()
	for _, p := range c.Panes {
		if p.Position.X != initial[p.Name] {
			t.Errorf("rotate + reverse should restore positions: %s was x=%d, now x=%d",
				p.Name, initial[p.Name], p.Position.X)
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 5: Window management
// ---------------------------------------------------------------------------

func TestStressMultiWindowWorkflow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Window 1: split vertically
	h.splitV()

	// Create window 2
	gen := h.generation()
	out := h.runCmd("new-window", "--name", "logs")
	if strings.Contains(out, "error") {
		t.Fatalf("new-window failed: %s", out)
	}
	h.waitLayout(gen)

	// Window 2: split horizontally
	h.splitH()

	// Create window 3
	gen = h.generation()
	h.runCmd("new-window", "--name", "debug")
	h.waitLayout(gen)

	// Verify list-windows shows all 3
	lw := h.runCmd("list-windows")
	if !strings.Contains(lw, "logs") {
		t.Errorf("list-windows should show 'logs', got:\n%s", lw)
	}
	if !strings.Contains(lw, "debug") {
		t.Errorf("list-windows should show 'debug', got:\n%s", lw)
	}

	// Switch windows: 3 → 1 → 2 → 3
	gen = h.generation()
	h.runCmd("select-window", "1")
	h.waitLayout(gen)

	c := h.captureJSON()
	if len(c.Panes) != 2 {
		t.Errorf("window 1 should have 2 panes, got %d", len(c.Panes))
	}

	gen = h.generation()
	h.runCmd("select-window", "logs")
	h.waitLayout(gen)

	c = h.captureJSON()
	if len(c.Panes) != 2 {
		t.Errorf("window 'logs' should have 2 panes, got %d", len(c.Panes))
	}

	gen = h.generation()
	h.runCmd("select-window", "debug")
	h.waitLayout(gen)

	c = h.captureJSON()
	if len(c.Panes) != 1 {
		t.Errorf("window 'debug' should have 1 pane, got %d", len(c.Panes))
	}
}

func TestStressWindowNextPrev(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Create 3 windows total
	gen := h.generation()
	h.runCmd("new-window", "--name", "win2")
	h.waitLayout(gen)

	gen = h.generation()
	h.runCmd("new-window", "--name", "win3")
	h.waitLayout(gen)

	// selectWindow runs a window navigation command and asserts the active window marker.
	selectWindow := func(cmd, wantMarker string) {
		t.Helper()
		gen = h.generation()
		h.runCmd(cmd)
		h.waitLayout(gen)

		lw := h.runCmd("list-windows")
		if !strings.Contains(lw, wantMarker) {
			t.Errorf("%s should show %s active, got:\n%s", cmd, wantMarker, lw)
		}
	}

	// Currently on window 3. prev → 2 → 1, then next → 2 → 3
	selectWindow("prev-window", "*2:")
	selectWindow("prev-window", "*1:")
	selectWindow("next-window", "*2:")
	selectWindow("next-window", "*3:")
}

func TestStressCrossWindowIsolation(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Window 1: echo a marker
	h.sendKeys("pane-1", "echo W1_MARKER", "Enter")
	h.waitFor("pane-1", "W1_MARKER")

	// Create window 2 and echo a different marker
	gen := h.generation()
	h.runCmd("new-window")
	h.waitLayout(gen)
	h.waitIdle("pane-2")

	h.sendKeys("pane-2", "echo W2_MARKER", "Enter")
	h.waitFor("pane-2", "W2_MARKER")

	// Verify window 2 pane doesn't contain window 1's marker
	p2out := h.runCmd("capture", "pane-2")
	if strings.Contains(p2out, "W1_MARKER") {
		t.Errorf("window 2 pane should not contain W1_MARKER, got:\n%s", p2out)
	}

	// Switch back to window 1, verify it doesn't have window 2's marker
	gen = h.generation()
	h.runCmd("select-window", "1")
	h.waitLayout(gen)

	p1out := h.runCmd("capture", "pane-1")
	if strings.Contains(p1out, "W2_MARKER") {
		t.Errorf("window 1 pane should not contain W2_MARKER, got:\n%s", p1out)
	}
}

// ---------------------------------------------------------------------------
// Phase 6: Spawn (agent panes)
// ---------------------------------------------------------------------------

func TestStressSpawnWithColor(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("spawn", "--name", "my-agent", "--task", "LAB-100", "--color", "f38ba8")
	if !strings.Contains(out, "my-agent") {
		t.Errorf("spawn should report name, got:\n%s", out)
	}

	c := h.captureJSON()
	p := h.jsonPane(c, "my-agent")
	if p.Task != "LAB-100" {
		t.Errorf("task should be LAB-100, got %q", p.Task)
	}
	if p.Color != "f38ba8" {
		t.Errorf("color should be f38ba8, got %q", p.Color)
	}

	// Verify the name and task show in list output
	list := h.runCmd("list")
	if !strings.Contains(list, "my-agent") {
		t.Errorf("list should show my-agent, got:\n%s", list)
	}
	if !strings.Contains(list, "LAB-100") {
		t.Errorf("list should show LAB-100, got:\n%s", list)
	}
}

// ---------------------------------------------------------------------------
// Phase 7: Content preservation across operations
// ---------------------------------------------------------------------------

func TestStressContentPreservationZoom(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Put identifiable content in pane-1
	h.sendKeys("pane-1", "echo ZOOM_PRESERVE_MARKER", "Enter")
	h.waitFor("pane-1", "ZOOM_PRESERVE_MARKER")

	// Zoom then unzoom
	h.runCmd("zoom", "pane-1")
	h.runCmd("zoom", "pane-1")

	after := h.runCmd("capture", "pane-1")
	if !strings.Contains(after, "ZOOM_PRESERVE_MARKER") {
		t.Errorf("content should be preserved after zoom/unzoom, got:\n%s", after)
	}
}

func TestStressContentPreservationMinimize(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()

	h.sendKeys("pane-1", "echo MINIMIZE_PRESERVE_MARKER", "Enter")
	h.waitFor("pane-1", "MINIMIZE_PRESERVE_MARKER")

	h.runCmd("minimize", "pane-1")
	h.runCmd("restore", "pane-1")

	after := h.runCmd("capture", "pane-1")
	if !strings.Contains(after, "MINIMIZE_PRESERVE_MARKER") {
		t.Errorf("content should be preserved after minimize/restore, got:\n%s", after)
	}
}

// ---------------------------------------------------------------------------
// Phase 8: Resize-window
// ---------------------------------------------------------------------------

func TestStressResizeWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Get initial dimensions
	c := h.captureJSON()
	initialW := c.Width
	initialH := c.Height

	// Resize to 120x40
	out := h.runCmd("resize-window", "120", "40")
	if strings.Contains(out, "error") || strings.Contains(out, "unknown") {
		t.Fatalf("resize-window failed: %s", out)
	}

	c = h.captureJSON()
	if c.Width != 120 {
		t.Errorf("width should be 120 after resize, got %d", c.Width)
	}
	// Height may be 39 or 40 depending on whether the global bar row is
	// counted in the capture. Accept either.
	if c.Height < 39 || c.Height > 40 {
		t.Errorf("height should be 39 or 40 after resize, got %d", c.Height)
	}

	// Panes should have adjusted — both still renderable
	for _, p := range c.Panes {
		if p.Position.Width <= 0 || p.Position.Height <= 0 {
			t.Errorf("%s has invalid dimensions after resize: %dx%d",
				p.Name, p.Position.Width, p.Position.Height)
		}
	}

	// Resize back to original
	h.runCmd("resize-window", strconv.Itoa(initialW), strconv.Itoa(initialH))

	c = h.captureJSON()
	if c.Width != initialW {
		t.Errorf("width should be %d after resize back, got %d", initialW, c.Width)
	}
}

// ---------------------------------------------------------------------------
// Phase 9: Synchronization primitives
// ---------------------------------------------------------------------------

func TestStressGenerationIncrementsOnLayoutChange(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	gen1 := h.generation()

	h.splitV()

	gen2 := h.generation()
	if gen2 <= gen1 {
		t.Errorf("generation should increase after split: %d → %d", gen1, gen2)
	}

	h.splitH()

	gen3 := h.generation()
	if gen3 <= gen2 {
		t.Errorf("generation should increase after second split: %d → %d", gen2, gen3)
	}
}

func TestStressWaitForTimeout(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for nonexistent content should timeout
	out := h.runCmd("wait-for", "pane-1", "THIS_WILL_NEVER_APPEAR", "--timeout", "1s")
	if !strings.Contains(out, "timeout") {
		t.Errorf("wait-for nonexistent content should timeout, got:\n%s", out)
	}
}

func TestStressWaitLayoutTimeout(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for a generation far in the future — should timeout
	futureGen := h.generation() + 1000
	out := h.runCmd("wait-layout", "--after", strconv.FormatUint(futureGen, 10), "--timeout", "1s")
	if !strings.Contains(out, "timeout") {
		t.Errorf("wait-layout for future generation should timeout, got:\n%s", out)
	}
}

func TestStressWaitIdleBusy(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Initial shell should become idle
	h.waitIdle("pane-1")

	c := h.captureJSON()
	p := h.jsonPane(c, "pane-1")
	if !p.Idle {
		t.Error("pane should be idle after waitIdle")
	}

	// Start a background sleep to make it busy
	h.startLongSleep("pane-1")
	h.waitBusy("pane-1")

	c = h.captureJSON()
	p = h.jsonPane(c, "pane-1")
	if p.Idle {
		t.Error("pane should be busy after starting sleep")
	}
}

// ---------------------------------------------------------------------------
// Phase 10: Events & Hooks
// ---------------------------------------------------------------------------

func TestStressEventsLayoutStream(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scanner, closer := eventStream(t, h.session, "--filter", "layout")
	defer closer()

	// Drain initial snapshot
	mustReadEvent(t, scanner, 5*time.Second)

	// Generate layout changes and verify events arrive
	h.doSplit("v")

	ev := mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("expected layout event after split, got %q", ev.Type)
	}
	if ev.Generation == 0 {
		t.Error("layout event should have non-zero generation")
	}

	// Kill a pane and verify another layout event
	h.runCmd("kill", "pane-2")

	ev = mustReadEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("expected layout event after kill, got %q", ev.Type)
	}
}

func TestStressHookLifecycle(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "hook-lifecycle")

	// Set a hook
	h.runCmd("set-hook", "on-idle", "touch "+marker)

	// Verify it's listed
	hooks := h.runCmd("list-hooks")
	if !strings.Contains(hooks, "on-idle") {
		t.Errorf("hook should be listed, got:\n%s", hooks)
	}

	// Trigger the hook by generating activity then waiting for idle
	h.sendKeys("pane-1", "echo HOOK_TRIGGER", "Enter")
	h.waitFor("pane-1", "HOOK_TRIGGER")

	if !waitForFile(t, marker, 5*time.Second) {
		t.Fatal("on-idle hook did not fire within timeout")
	}

	// Remove and clean up
	os.Remove(marker)
	h.runCmd("unset-hook", "on-idle")

	hooks = h.runCmd("list-hooks")
	if strings.Contains(hooks, "on-idle") {
		t.Errorf("hook should be removed after unset, got:\n%s", hooks)
	}

	// Trigger activity again — hook should NOT fire.
	// waitIdle blocks until the idle transition completes (>= DefaultIdleTimeout),
	// which is when the hook would have fired. No sleep needed.
	h.sendKeys("pane-1", "echo AFTER_UNSET", "Enter")
	h.waitFor("pane-1", "AFTER_UNSET")
	h.waitIdle("pane-1")

	if _, err := os.Stat(marker); err == nil {
		t.Error("hook should not fire after unset")
	}
}

// ---------------------------------------------------------------------------
// Phase 11: Keybinding sweep (AmuxHarness)
// ---------------------------------------------------------------------------

func TestStressKeybindingSplits(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	splits := []struct {
		key       string
		wantPanes int
		desc      string
	}{
		{`\`, 2, "vertical split"},
		{"-", 3, "horizontal split"},
		{"|", 4, "root vertical split"},
		{"_", 5, "root horizontal split"},
	}

	for _, tt := range splits {
		gen := h.generation()
		h.sendKeys("C-a", tt.key)
		h.waitLayout(gen)

		c := h.captureJSON()
		if len(c.Panes) != tt.wantPanes {
			t.Fatalf("Ctrl-a %s (%s): expected %d panes, got %d",
				tt.key, tt.desc, tt.wantPanes, len(c.Panes))
		}
	}

	// Verify the first split created a vertical layout (left|right)
	c := h.captureJSON()
	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	if p1.Position.X >= p2.Position.X {
		t.Error("Ctrl-a \\ should create vertical split (left|right)")
	}
}

func TestStressKeybindingNavigation(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Create 2x2 grid: pane-1|pane-2 top, pane-3 below pane-2, pane-4 (root hsplit)
	h.splitV()     // pane-1 | pane-2
	h.splitH()     // pane-2 splits → pane-2 (top-right) / pane-3 (bottom-right)
	h.splitRootH() // root hsplit → pane-4 at bottom

	// Cycle through all panes with Ctrl-a o
	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		gen := h.generation()
		h.sendKeys("C-a", "o")
		h.waitLayout(gen)
		seen[h.activePaneName()] = true
	}
	for _, name := range []string{"pane-1", "pane-2", "pane-3", "pane-4"} {
		if !seen[name] {
			t.Errorf("Ctrl-a o cycle never reached %s (saw: %v)", name, seen)
		}
	}

	// Navigate with h/j/k/l
	h.runCmd("focus", "pane-1")

	gen := h.generation()
	h.sendKeys("C-a", "l") // right
	h.waitLayout(gen)

	active := h.activePaneName()
	if active != "pane-2" && active != "pane-3" {
		t.Errorf("Ctrl-a l from pane-1 should move right, got %s", active)
	}
}

func TestStressKeybindingWindowOps(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// sendKeyAndCheck sends a keybinding and verifies the expected window marker.
	sendKeyAndCheck := func(key, desc, wantMarker string) {
		t.Helper()
		gen := h.generation()
		h.sendKeys("C-a", key)
		h.waitLayout(gen)

		lw := h.runCmd("list-windows")
		if !strings.Contains(lw, wantMarker) {
			t.Errorf("Ctrl-a %s (%s) should show %s, got:\n%s", key, desc, wantMarker, lw)
		}
	}

	// Create windows 2 and 3
	sendKeyAndCheck("c", "new window", "2:")
	sendKeyAndCheck("c", "new window", "3:")

	// Navigate: prev → window 2, next → window 3, direct select → window 1
	sendKeyAndCheck("p", "previous window", "*2:")
	sendKeyAndCheck("n", "next window", "*3:")
	sendKeyAndCheck("1", "select window 1", "*1:")
}

func TestStressKeybindingResize(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	// Focus pane-1 (left)
	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	c := h.captureJSON()
	initialW := h.jsonPane(c, "pane-1").Position.Width

	// Ctrl-a L → grow right (3 times)
	for i := 0; i < 3; i++ {
		gen = h.generation()
		h.sendKeys("C-a", "L")
		h.waitLayout(gen)
	}

	c = h.captureJSON()
	grownW := h.jsonPane(c, "pane-1").Position.Width
	if grownW <= initialW {
		t.Errorf("3x Ctrl-a L should grow pane-1: was %d, now %d", initialW, grownW)
	}
}

func TestStressKeybindingSwap(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	c := h.captureJSON()
	p1Before := h.jsonPane(c, "pane-1").Position.X
	p2Before := h.jsonPane(c, "pane-2").Position.X

	// Ctrl-a } → swap forward
	gen := h.generation()
	h.sendKeys("C-a", "}")
	h.waitLayout(gen)

	c = h.captureJSON()
	p1After := h.jsonPane(c, "pane-1").Position.X
	p2After := h.jsonPane(c, "pane-2").Position.X

	if p1After == p1Before && p2After == p2Before {
		t.Error("Ctrl-a } should swap pane positions")
	}

	// Ctrl-a { → swap backward (should restore)
	gen = h.generation()
	h.sendKeys("C-a", "{")
	h.waitLayout(gen)

	c = h.captureJSON()
	p1Restored := h.jsonPane(c, "pane-1").Position.X
	p2Restored := h.jsonPane(c, "pane-2").Position.X

	if p1Restored != p1Before || p2Restored != p2Before {
		t.Errorf("Ctrl-a { should restore positions: p1 %d→%d→%d, p2 %d→%d→%d",
			p1Before, p1After, p1Restored, p2Before, p2After, p2Restored)
	}
}

func TestStressKeybindingLiteralPrefix(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Ctrl-a Ctrl-a should send literal Ctrl-a to the shell
	// In bash, Ctrl-a moves cursor to beginning of line. Type something,
	// then Ctrl-a Ctrl-a to go to beginning, then type a prefix.
	h.sendKeys("e", "c", "h", "o", " ", "T", "E", "S", "T")

	// Send literal Ctrl-a (goes to beginning of line in bash readline).
	// Keys are processed in order by the PTY, so no sleep needed.
	h.sendKeys("C-a", "C-a")

	// Type a # at the beginning to comment it out, then Enter
	h.sendKeys("#", "Enter")

	// The shell should have executed "#echo TEST" which is a comment (no output)
	// Verify by typing another echo
	h.sendKeys("e", "c", "h", "o", " ", "L", "I", "T", "E", "R", "A", "L", "_", "O", "K", "Enter")

	if !h.waitFor("LITERAL_OK", 3*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("literal Ctrl-a should work (cursor to BOL)\nScreen:\n%s", screen)
	}
}

// ---------------------------------------------------------------------------
// Phase 12: Copy mode via keybinding
// ---------------------------------------------------------------------------

func TestStressCopyModeKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Enter copy mode with Ctrl-a [
	h.sendKeys("C-a", "[")
	if !h.waitFor("[copy]", 3*time.Second) {
		t.Fatalf("expected [copy] after Ctrl-a [\nScreen:\n%s", h.captureOuter())
	}

	// Movement: j/k should work without crashing
	h.sendKeys("j", "j", "k")

	// Still in copy mode
	if !h.waitFor("[copy]", 1*time.Second) {
		t.Fatalf("expected [copy] after movement\nScreen:\n%s", h.captureOuter())
	}

	// Exit with Escape
	h.sendKeys("Escape")
	if !waitForOuter(h, func(s string) bool {
		return !strings.Contains(s, "[copy]")
	}, 3*time.Second) {
		t.Fatalf("Escape should exit copy mode\nScreen:\n%s", h.captureOuter())
	}
}

// ---------------------------------------------------------------------------
// Phase 13: Edge case combos
// ---------------------------------------------------------------------------

func TestStressZoomPlusSplitAutoUnzooms(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitH()

	// Zoom pane-2
	gen := h.generation()
	h.sendKeys("C-a", "z")
	h.waitLayout(gen)

	h.assertScreen("zoomed: only pane-2 visible", func(s string) bool {
		return strings.Contains(s, "[pane-2]") && !strings.Contains(s, "[pane-1]")
	})

	// Split while zoomed → should auto-unzoom
	gen = h.generation()
	h.sendKeys("C-a", "-")
	h.waitLayout(gen)

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("split while zoomed should create pane-3: got %d panes", len(c.Panes))
	}

	// All panes should be visible (unzoomed)
	for _, p := range c.Panes {
		if p.Zoomed {
			t.Errorf("%s should not be zoomed after split", p.Name)
		}
	}
}

func TestStressKillZoomedPaneClearsZoom(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitH()
	h.splitH()

	// Zoom pane-2
	h.runCmd("zoom", "pane-2")

	// Kill the zoomed pane
	h.runCmd("kill", "pane-2")

	status := h.runCmd("status")
	if strings.Contains(status, "zoomed") {
		t.Errorf("zoom should be cleared after killing zoomed pane, got:\n%s", status)
	}

	// Remaining panes should be visible
	h.assertScreen("remaining panes visible", func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-3]")
	})
}

func TestStressMultipleWindowsWithSplits(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Window 1: 2 vertical panes
	h.splitV()

	// Window 2: 3 horizontal panes
	gen := h.generation()
	h.runCmd("new-window", "--name", "three-pane")
	h.waitLayout(gen)
	h.splitH()
	h.splitH()

	// Window 3: single pane with custom name
	gen = h.generation()
	h.runCmd("new-window", "--name", "single")
	h.waitLayout(gen)

	// Switch between windows and verify pane counts
	tests := []struct {
		window    string
		paneCount int
	}{
		{"1", 2},
		{"three-pane", 3},
		{"single", 1},
	}

	for _, tt := range tests {
		gen = h.generation()
		h.runCmd("select-window", tt.window)
		h.waitLayout(gen)

		c := h.captureJSON()
		if len(c.Panes) != tt.paneCount {
			t.Errorf("window %s: expected %d panes, got %d", tt.window, tt.paneCount, len(c.Panes))
		}
	}
}

func TestStressMinimizeOnVerticalSplitFails(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	out := h.runCmd("minimize", "pane-1")
	if !strings.Contains(out, "cannot") {
		t.Errorf("minimize on vertical split should fail with 'cannot', got:\n%s", out)
	}

	// Verify no panes are minimized
	c := h.captureJSON()
	for _, p := range c.Panes {
		if p.Minimized {
			t.Errorf("%s should not be minimized", p.Name)
		}
	}
}

func TestStressFocusNonexistent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("focus", "nonexistent-pane")
	if !strings.Contains(out, "not found") {
		t.Errorf("focus nonexistent should report error, got:\n%s", out)
	}

	// Active pane should be unchanged
	h.assertActive("pane-1")
}

func TestStressFocusDirections(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// focusAndAssert runs a directional focus and verifies the active pane.
	focusAndAssert := func(direction, wantActive string) {
		t.Helper()
		out := h.doFocus(direction)
		if strings.Contains(out, "error") {
			t.Errorf("focus %s failed: %s", direction, out)
		}
		h.assertActive(wantActive)
	}

	focusAndAssert("left", "pane-1")
	focusAndAssert("right", "pane-2")

	// Add horizontal split for up/down testing
	h.splitH()

	// pane-3 is active (below pane-2)
	focusAndAssert("up", "pane-2")
	focusAndAssert("down", "pane-3")

	// Focus next (cycle) -- just verify no error
	out := h.doFocus("next")
	if strings.Contains(out, "error") {
		t.Errorf("focus next failed: %s", out)
	}
}

func TestStressRenameWindow(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("rename-window", "test-renamed")

	lw := h.runCmd("list-windows")
	if !strings.Contains(lw, "test-renamed") {
		t.Errorf("window should be renamed, got:\n%s", lw)
	}

	// Can select by new name
	gen := h.generation()
	h.runCmd("new-window")
	h.waitLayout(gen)

	gen = h.generation()
	h.runCmd("select-window", "test-renamed")
	h.waitLayout(gen)

	lw = h.runCmd("list-windows")
	if !strings.Contains(lw, "*1:") || !strings.Contains(lw, "test-renamed") {
		t.Errorf("should be on renamed window 1, got:\n%s", lw)
	}
}

func TestStressSendKeysHex(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Send "echo HEX_OK\r" as hex bytes
	// "echo HEX_OK\r" = 65 63 68 6f 20 48 45 58 5f 4f 4b 0d
	out := h.runCmd("send-keys", "pane-1", "--hex", "6563686f204845585f4f4b0d")
	if strings.Contains(out, "error") {
		t.Fatalf("send-keys --hex failed: %s", out)
	}

	h.waitFor("pane-1", "HEX_OK")
}

func TestStressDetachKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Ctrl-a d should detach the inner client
	h.sendKeys("C-a", "d")

	// After detach, the outer pane shows the shell prompt (no amux chrome)
	h.outer.waitFor("pane-1", "$")

	outerContent := h.captureOuter()
	if strings.Contains(outerContent, "amux") && strings.Contains(outerContent, "panes") {
		t.Errorf("inner amux should be detached\nOuter:\n%s", outerContent)
	}
}

func TestStressRootSplitGrid(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Build a 2x2 grid using root splits
	h.splitRootV() // pane-1 | pane-2
	h.splitRootH() // top: pane-1|pane-2, bottom: pane-3

	c := h.captureJSON()
	if len(c.Panes) != 3 {
		t.Fatalf("expected 3 panes, got %d", len(c.Panes))
	}

	p1 := h.jsonPane(c, "pane-1")
	p2 := h.jsonPane(c, "pane-2")
	p3 := h.jsonPane(c, "pane-3")

	// p1 left of p2 (vertical split)
	if p1.Position.X >= p2.Position.X {
		t.Errorf("pane-1 (x=%d) should be left of pane-2 (x=%d)", p1.Position.X, p2.Position.X)
	}

	// p3 below the top row (root horizontal split)
	topMaxY := p1.Position.Y
	if p2.Position.Y > topMaxY {
		topMaxY = p2.Position.Y
	}
	if p3.Position.Y <= topMaxY {
		t.Errorf("pane-3 (y=%d) should be below top row (max y=%d)", p3.Position.Y, topMaxY)
	}
}

func TestStressPaneIsolation(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV()

	// Echo unique markers in each pane
	h.sendKeys("pane-1", "echo PANE1_UNIQUE_MARKER", "Enter")
	h.waitFor("pane-1", "PANE1_UNIQUE_MARKER")

	h.sendKeys("pane-2", "echo PANE2_UNIQUE_MARKER", "Enter")
	h.waitFor("pane-2", "PANE2_UNIQUE_MARKER")

	// Cross-verify: pane-1 shouldn't have pane-2's marker
	p1 := h.runCmd("capture", "pane-1")
	if strings.Contains(p1, "PANE2_UNIQUE_MARKER") {
		t.Errorf("pane-1 should not contain PANE2_UNIQUE_MARKER, got:\n%s", p1)
	}

	p2 := h.runCmd("capture", "pane-2")
	if strings.Contains(p2, "PANE1_UNIQUE_MARKER") {
		t.Errorf("pane-2 should not contain PANE1_UNIQUE_MARKER, got:\n%s", p2)
	}
}
