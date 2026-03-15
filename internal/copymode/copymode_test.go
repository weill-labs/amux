package copymode

import (
	"fmt"
	"strings"
	"testing"
)

// fakeEmulator implements TerminalEmulator for testing.
type fakeEmulator struct {
	width, height int
	screen        []string // current screen lines (plain text)
	scrollback    []string // scrollback lines (0=oldest)
}

func newFakeEmulator(w, h int) *fakeEmulator {
	screen := make([]string, h)
	for i := range screen {
		screen[i] = ""
	}
	return &fakeEmulator{width: w, height: h, screen: screen}
}

func (e *fakeEmulator) Render() string {
	return strings.Join(e.screen, "\n")
}

func (e *fakeEmulator) Size() (int, int) {
	return e.width, e.height
}

func (e *fakeEmulator) ScrollbackLen() int {
	return len(e.scrollback)
}

func (e *fakeEmulator) ScrollbackLineText(y int) string {
	if y < 0 || y >= len(e.scrollback) {
		return ""
	}
	return e.scrollback[y]
}

func TestNewCopyMode(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24)

	if cm.ScrollOffset() != 0 {
		t.Errorf("initial oy = %d, want 0", cm.ScrollOffset())
	}
	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 0 {
		t.Errorf("initial cursor = (%d,%d), want (0,0)", cx, cy)
	}
}

func TestScrollUpDown(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	// Add 20 lines of scrollback
	for i := 0; i < 20; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("scrollback-line-%d", i))
	}
	cm := New(emu, 80, 10)

	// k scrolls up
	action := cm.HandleInput([]byte{'k'})
	if action != ActionRedraw {
		t.Errorf("k should return ActionRedraw, got %d", action)
	}
	if cm.ScrollOffset() != 1 {
		t.Errorf("after k: oy = %d, want 1", cm.ScrollOffset())
	}

	// j scrolls down
	action = cm.HandleInput([]byte{'j'})
	if action != ActionRedraw {
		t.Errorf("j should return ActionRedraw, got %d", action)
	}
	if cm.ScrollOffset() != 0 {
		t.Errorf("after j: oy = %d, want 0", cm.ScrollOffset())
	}

	// j at bottom does nothing
	action = cm.HandleInput([]byte{'j'})
	if action != ActionNone {
		t.Errorf("j at bottom should return ActionNone, got %d", action)
	}
}

func TestScrollToTopBottom(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 50; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10)

	// g → scroll to top
	cm.HandleInput([]byte{'g'})
	if cm.ScrollOffset() != 50 {
		t.Errorf("after g: oy = %d, want 50", cm.ScrollOffset())
	}

	// G → scroll to bottom
	cm.HandleInput([]byte{'G'})
	if cm.ScrollOffset() != 0 {
		t.Errorf("after G: oy = %d, want 0", cm.ScrollOffset())
	}
}

func TestExitKeys(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24)

	if action := cm.HandleInput([]byte{'q'}); action != ActionExit {
		t.Errorf("q should return ActionExit, got %d", action)
	}

	cm2 := New(emu, 80, 24)
	if action := cm2.HandleInput([]byte{0x1b}); action != ActionExit {
		t.Errorf("Escape should return ActionExit, got %d", action)
	}
}

func TestSearchBasic(t *testing.T) {
	emu := newFakeEmulator(80, 5)
	emu.scrollback = []string{
		"first line",
		"hello world",
		"another line",
		"hello again",
		"last line",
	}
	cm := New(emu, 80, 5)

	// Enter search mode
	cm.HandleInput([]byte{'/'})
	if !cm.IsSearching() {
		t.Fatal("expected searching=true after /")
	}

	// Type "hello"
	cm.HandleInput([]byte("hello"))
	if cm.SearchBarText() != "/hello" {
		t.Errorf("search bar = %q, want /hello", cm.SearchBarText())
	}

	// Confirm search
	cm.HandleInput([]byte{'\r'})
	if cm.IsSearching() {
		t.Fatal("expected searching=false after Enter")
	}
	if cm.SearchQuery() != "hello" {
		t.Errorf("query = %q, want hello", cm.SearchQuery())
	}
}

func TestSearchCancel(t *testing.T) {
	emu := newFakeEmulator(80, 5)
	cm := New(emu, 80, 5)

	cm.HandleInput([]byte{'/'})
	cm.HandleInput([]byte("test"))
	cm.HandleInput([]byte{0x1b}) // Escape

	if cm.IsSearching() {
		t.Fatal("expected searching=false after Escape")
	}
	if cm.SearchQuery() != "" {
		t.Errorf("query should be empty after cancel, got %q", cm.SearchQuery())
	}
}

func TestHalfPageScroll(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 30; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10)

	// Ctrl-u → half page up
	cm.HandleInput([]byte{0x15})
	if cm.ScrollOffset() != 5 {
		t.Errorf("after Ctrl-u: oy = %d, want 5", cm.ScrollOffset())
	}

	// Ctrl-d → half page down
	cm.HandleInput([]byte{0x04})
	if cm.ScrollOffset() != 0 {
		t.Errorf("after Ctrl-d: oy = %d, want 0", cm.ScrollOffset())
	}
}

func TestRenderViewport(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.scrollback = []string{"sb-line-0", "sb-line-1"}
	emu.screen = []string{"screen-0", "screen-1", "screen-2"}
	cm := New(emu, 20, 3)

	// At oy=0, should show the last 3 lines (screen lines)
	rendered := cm.RenderViewport()
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// First line (cursor) should have reverse video
	if !strings.Contains(lines[0], "\033[7m") {
		t.Errorf("cursor line should have reverse video, got: %q", lines[0])
	}

	// Scroll up to see scrollback
	cm.HandleInput([]byte{'g'}) // go to top
	rendered = cm.RenderViewport()
	lines = strings.Split(rendered, "\n")
	// Should now show scrollback lines
	if !strings.Contains(lines[0], "sb-line-0") {
		t.Errorf("top of scroll should show sb-line-0, got: %q", lines[0])
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"\033[31mred\033[0m", "red"},
		{"\033[38;2;100;200;255mcolor\033[0m text", "color text"},
		{"\033]0;title\007rest", "rest"},
	}
	for _, tt := range tests {
		got := stripANSI(tt.input)
		if got != tt.want {
			t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
