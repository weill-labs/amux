package copymode

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
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

func (e *fakeEmulator) ScreenLineText(y int) string {
	if y < 0 || y >= len(e.screen) {
		return ""
	}
	return e.screen[y]
}

func TestNewCopyMode(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24, 0)

	if cm.ScrollOffset() != 0 {
		t.Errorf("initial oy = %d, want 0", cm.ScrollOffset())
	}
	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 0 {
		t.Errorf("initial cursor = (%d,%d), want (0,0)", cx, cy)
	}
}

func TestNewCopyModeCursorPosition(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24, 10)

	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 10 {
		t.Errorf("cursor = (%d,%d), want (0,10)", cx, cy)
	}
}

func TestNewCopyModeCursorClamped(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24, 100) // beyond viewport

	_, cy := cm.CursorPos()
	if cy != 23 {
		t.Errorf("cursor cy = %d, want 23 (clamped to height-1)", cy)
	}
}

func TestCursorMovement(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 20; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("scrollback-line-%d", i))
	}
	cm := New(emu, 80, 10, 5) // cursor in middle

	// j moves cursor down
	action := cm.HandleInput([]byte{'j'})
	if action != ActionRedraw {
		t.Errorf("j should return ActionRedraw, got %d", action)
	}
	_, cy := cm.CursorPos()
	if cy != 6 {
		t.Errorf("after j: cy = %d, want 6", cy)
	}

	// k moves cursor up
	action = cm.HandleInput([]byte{'k'})
	if action != ActionRedraw {
		t.Errorf("k should return ActionRedraw, got %d", action)
	}
	_, cy = cm.CursorPos()
	if cy != 5 {
		t.Errorf("after k: cy = %d, want 5", cy)
	}
}

func TestCursorEdgeScrolling(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 20; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("scrollback-line-%d", i))
	}
	cm := New(emu, 80, 10, 0) // cursor at top

	// k at top of viewport scrolls up (increases oy)
	action := cm.HandleInput([]byte{'k'})
	if action != ActionRedraw {
		t.Errorf("k at top should return ActionRedraw, got %d", action)
	}
	if cm.ScrollOffset() != 1 {
		t.Errorf("after k at top: oy = %d, want 1", cm.ScrollOffset())
	}
	_, cy := cm.CursorPos()
	if cy != 0 {
		t.Errorf("cursor should stay at 0 after edge scroll, got %d", cy)
	}

	// j scrolls back down (cursor at top, oy > 0)
	cm.HandleInput([]byte{'j'})
	_, cy = cm.CursorPos()
	if cy != 1 {
		t.Errorf("after j from top: cy = %d, want 1", cy)
	}
}

func TestCursorBottomEdgeScrolling(t *testing.T) {
	emu := newFakeEmulator(80, 5)
	for i := 0; i < 10; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	// Start scrolled up with cursor at bottom
	cm := New(emu, 80, 5, 4)
	cm.HandleInput([]byte{'g'}) // scroll to top
	_, cy := cm.CursorPos()
	if cy != 0 {
		t.Errorf("after g: cy = %d, want 0", cy)
	}

	// Move cursor to bottom of viewport
	for i := 0; i < 4; i++ {
		cm.HandleInput([]byte{'j'})
	}
	_, cy = cm.CursorPos()
	if cy != 4 {
		t.Errorf("cursor at bottom: cy = %d, want 4", cy)
	}

	// j at bottom of viewport should scroll (decrease oy)
	oyBefore := cm.ScrollOffset()
	cm.HandleInput([]byte{'j'})
	if cm.ScrollOffset() != oyBefore-1 {
		t.Errorf("j at bottom should scroll: oy = %d, want %d", cm.ScrollOffset(), oyBefore-1)
	}
	_, cy = cm.CursorPos()
	if cy != 4 {
		t.Errorf("cursor should stay at bottom after edge scroll, got %d", cy)
	}
}

func TestHorizontalMovement(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	cm := New(emu, 80, 10, 0)

	// l moves cursor right
	cm.HandleInput([]byte{'l'})
	cx, _ := cm.CursorPos()
	if cx != 1 {
		t.Errorf("after l: cx = %d, want 1", cx)
	}

	// h moves cursor left
	cm.HandleInput([]byte{'h'})
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Errorf("after h: cx = %d, want 0", cx)
	}

	// h at left edge does nothing
	action := cm.HandleInput([]byte{'h'})
	if action != ActionNone {
		t.Errorf("h at left edge should return ActionNone, got %d", action)
	}

	// l to right edge
	for i := 0; i < 79; i++ {
		cm.HandleInput([]byte{'l'})
	}
	cx, _ = cm.CursorPos()
	if cx != 79 {
		t.Errorf("at right edge: cx = %d, want 79", cx)
	}

	// l at right edge does nothing
	action = cm.HandleInput([]byte{'l'})
	if action != ActionNone {
		t.Errorf("l at right edge should return ActionNone, got %d", action)
	}
}

func TestScrollToTopBottom(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 50; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10, 5)

	// g → scroll to top, cursor to row 0
	cm.HandleInput([]byte{'g'})
	if cm.ScrollOffset() != 50 {
		t.Errorf("after g: oy = %d, want 50", cm.ScrollOffset())
	}
	_, cy := cm.CursorPos()
	if cy != 0 {
		t.Errorf("after g: cy = %d, want 0", cy)
	}

	// G → scroll to bottom, cursor to last row
	cm.HandleInput([]byte{'G'})
	if cm.ScrollOffset() != 0 {
		t.Errorf("after G: oy = %d, want 0", cm.ScrollOffset())
	}
	_, cy = cm.CursorPos()
	if cy != 9 {
		t.Errorf("after G: cy = %d, want 9", cy)
	}
}

func TestExitKeys(t *testing.T) {
	emu := newFakeEmulator(80, 24)
	cm := New(emu, 80, 24, 0)

	if action := cm.HandleInput([]byte{'q'}); action != ActionExit {
		t.Errorf("q should return ActionExit, got %d", action)
	}

	cm2 := New(emu, 80, 24, 0)
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
	cm := New(emu, 80, 5, 0)

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
	cm := New(emu, 80, 5, 0)

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
	cm := New(emu, 80, 10, 0)

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
	cm := New(emu, 20, 3, 0)

	// At oy=0, should show the last 3 lines (screen lines)
	rendered := cm.RenderViewport()
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Cursor character at (0,0) should have reverse video
	if !strings.Contains(lines[0], "\033[7m") {
		t.Errorf("cursor character should have reverse video, got: %q", lines[0])
	}
	// Non-cursor lines should NOT have reverse video
	if strings.Contains(lines[1], "\033[7m") {
		t.Errorf("non-cursor line should not have reverse video, got: %q", lines[1])
	}

	// Scroll up to see scrollback
	cm.HandleInput([]byte{'g'}) // go to top
	rendered = cm.RenderViewport()
	lines = strings.Split(rendered, "\n")
	// Should now show scrollback lines (cursor is on first char, so check for "b-line-0" after cursor escape)
	if !strings.Contains(lines[0], "b-line-0") {
		t.Errorf("top of scroll should show sb-line-0, got: %q", lines[0])
	}
}

func TestRenderCursorSingleChar(t *testing.T) {
	emu := newFakeEmulator(10, 3)
	emu.screen = []string{"hello", "world", "test!"}
	cm := New(emu, 10, 3, 1) // cursor on row 1
	cm.cx = 2                // cursor on column 2

	rendered := cm.RenderViewport()
	lines := strings.Split(rendered, "\n")

	// Row 1 should have reverse video around just the 'r' (column 2 of "world")
	if !strings.Contains(lines[1], reverseOn+"r"+reverseOff) {
		t.Errorf("expected single-char cursor on 'r', got: %q", lines[1])
	}
	// Row 0 should NOT have reverse video
	if strings.Contains(lines[0], reverseOn) {
		t.Errorf("non-cursor row should not have reverse video, got: %q", lines[0])
	}
}

func TestSelectionHighlighting(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world foo", "second line here", "third line text"}
	cm := New(emu, 20, 3, 0)

	// Move to column 6, then start selection with v
	for i := 0; i < 6; i++ {
		cm.HandleInput([]byte{'l'})
	}
	cm.HandleInput([]byte{'v'})

	// Move down and right to extend selection
	cm.HandleInput([]byte{'j'})
	cm.HandleInput([]byte{'l'})
	cm.HandleInput([]byte{'l'})

	rendered := cm.RenderViewport()
	lines := strings.Split(rendered, "\n")

	// First line should have selection highlight starting at column 6
	if !strings.Contains(lines[0], selectionBg) {
		t.Errorf("first selected line should have selection bg, got: %q", lines[0])
	}
	// Second line should have selection highlight
	if !strings.Contains(lines[1], selectionBg) {
		t.Errorf("second selected line should have selection bg, got: %q", lines[1])
	}
	// Third line should NOT have selection highlight
	if strings.Contains(lines[2], selectionBg) {
		t.Errorf("unselected line should not have selection bg, got: %q", lines[2])
	}
}

func TestLineSelectHighlighting(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world foo", "second line here", "third line text"}
	cm := New(emu, 20, 3, 0)

	// Move to column 6 (should not affect line-select range)
	for i := 0; i < 6; i++ {
		cm.HandleInput([]byte{'l'})
	}

	// V to enter line-select mode
	cm.HandleInput([]byte{'V'})
	if !cm.selecting || !cm.lineSelect {
		t.Fatal("expected selecting=true, lineSelect=true after V")
	}

	rendered := cm.RenderViewport()
	lines := strings.Split(rendered, "\n")

	// First line should be fully highlighted (selection covers entire line)
	if !strings.Contains(lines[0], selectionBg) {
		t.Errorf("selected line should have selection bg, got: %q", lines[0])
	}
	// Second line should NOT have selection highlight
	if strings.Contains(lines[1], selectionBg) {
		t.Errorf("unselected line should not have selection bg, got: %q", lines[1])
	}

	// Extend selection down
	cm.HandleInput([]byte{'j'})
	rendered = cm.RenderViewport()
	lines = strings.Split(rendered, "\n")

	// Both lines 0 and 1 should have full-line highlighting
	if !strings.Contains(lines[0], selectionBg) {
		t.Errorf("first selected line should have selection bg, got: %q", lines[0])
	}
	if !strings.Contains(lines[1], selectionBg) {
		t.Errorf("second selected line should have selection bg, got: %q", lines[1])
	}
	if strings.Contains(lines[2], selectionBg) {
		t.Errorf("unselected line should not have selection bg, got: %q", lines[2])
	}
}

func TestLineSelectYank(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world", "second line", "third line"}
	cm := New(emu, 20, 3, 0)

	// Move to column 5 (irrelevant for line-select)
	for i := 0; i < 5; i++ {
		cm.HandleInput([]byte{'l'})
	}

	// V then j to select two full lines
	cm.HandleInput([]byte{'V'})
	cm.HandleInput([]byte{'j'})

	text := cm.SelectedText()
	expected := "hello world\nsecond line\n"
	if text != expected {
		t.Errorf("line-select yank = %q, want %q", text, expected)
	}
}

func TestLineSelectToggleOff(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello", "world", "test"}
	cm := New(emu, 20, 3, 0)

	// V on, then V off
	cm.HandleInput([]byte{'V'})
	if !cm.selecting {
		t.Fatal("expected selecting=true after V")
	}
	cm.HandleInput([]byte{'V'})
	if cm.selecting || cm.lineSelect {
		t.Fatal("expected selecting=false, lineSelect=false after second V")
	}
}

func TestVClearsLineSelect(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello", "world", "test"}
	cm := New(emu, 20, 3, 0)

	// V then v should switch to character selection
	cm.HandleInput([]byte{'V'})
	if !cm.lineSelect {
		t.Fatal("expected lineSelect=true after V")
	}
	cm.HandleInput([]byte{'v'})
	if cm.lineSelect {
		t.Fatal("expected lineSelect=false after v")
	}
	if !cm.selecting {
		t.Fatal("expected selecting=true after v (character select started)")
	}
}

func TestBatchedInputVy(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world", "second line", "third line"}
	cm := New(emu, 20, 3, 0)

	// Send V and y as a single batch — both must be processed.
	action := cm.HandleInput([]byte{'V', 'y'})
	if action != ActionYank {
		t.Errorf("batched Vy should return ActionYank, got %d", action)
	}

	text := cm.SelectedText()
	if text == "" {
		t.Fatal("batched Vy should produce non-empty selection")
	}
	if text != "hello world\n" {
		t.Errorf("batched Vy text = %q, want %q", text, "hello world\n")
	}
}

func TestBatchedInputMovement(t *testing.T) {
	emu := newFakeEmulator(20, 5)
	emu.screen = []string{"line-0", "line-1", "line-2", "line-3", "line-4"}
	cm := New(emu, 20, 5, 0)

	// Send 5 'l' keys as a single batch — cursor should move 5 columns.
	action := cm.HandleInput([]byte{'l', 'l', 'l', 'l', 'l'})
	if action != ActionRedraw {
		t.Errorf("batched lllll should return ActionRedraw, got %d", action)
	}
	cx, _ := cm.CursorPos()
	if cx != 5 {
		t.Errorf("cursor after 5 batched l's: cx = %d, want 5", cx)
	}
}

func TestBatchedInputSearchThenNormal(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.scrollback = []string{"hello world"}
	emu.screen = []string{"screen-0", "screen-1", "screen-2"}
	cm := New(emu, 20, 3, 0)

	// Batch: '/' enters search, then "hi\r" confirms, then remaining bytes
	// are processed as normal mode keys. After search confirm, 'j' should move.
	action := cm.HandleInput([]byte{'/', 'h', 'i', '\r', 'j'})
	if action != ActionRedraw {
		t.Errorf("expected ActionRedraw, got %d", action)
	}
	if cm.SearchQuery() != "hi" {
		t.Errorf("search query = %q, want %q", cm.SearchQuery(), "hi")
	}
}

func TestCellAt_CursorReverse(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(10, 3)
	emu.screen = []string{"hello", "world", "test!"}
	cm := New(emu, 10, 3, 1) // cursor row 1
	cm.cx = 2                // cursor col 2

	cell := cm.CellAt(2, 1)
	if cell.Char != "r" {
		t.Errorf("cursor cell Char = %q, want 'r'", cell.Char)
	}
	if cell.Style.Attrs&uv.AttrReverse == 0 {
		t.Error("cursor cell should have AttrReverse")
	}

	// Non-cursor cell should not have reverse.
	other := cm.CellAt(0, 0)
	if other.Style.Attrs&uv.AttrReverse != 0 {
		t.Error("non-cursor cell should not have AttrReverse")
	}
}

func TestCellAt_Selection(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(10, 3)
	emu.screen = []string{"hello", "world", "test!"}
	cm := New(emu, 10, 3, 0)

	// Start character selection at (2,0), extend to (4,0).
	cm.cx = 2
	cm.HandleInput([]byte{'v'})
	cm.HandleInput([]byte{'l', 'l'})

	// Col 3 should be selected.
	cell := cm.CellAt(3, 0)
	if cell.Style.Bg == nil {
		t.Error("selected cell should have non-nil Bg")
	}

	// Col 0 should NOT be selected.
	unsel := cm.CellAt(0, 0)
	if unsel.Style.Bg != nil {
		t.Error("unselected cell should have nil Bg")
	}
}

func TestCellAt_SearchMatch(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world", "foo bar", "hello again"}
	cm := New(emu, 20, 3, 0)

	// Search for "hello"
	cm.HandleInput([]byte{'/'})
	cm.HandleInput([]byte("hello"))
	cm.HandleInput([]byte{'\r'})

	// Col 0 of row 0 should be in a match.
	cell := cm.CellAt(0, 0)
	if cell.Style.Bg == nil {
		t.Error("match cell should have non-nil Bg")
	}

	// Col 6 of row 0 (after "hello ") should NOT be in a match.
	noMatch := cm.CellAt(6, 0)
	if noMatch.Style.Bg != nil {
		t.Error("non-match cell should have nil Bg")
	}
}

// --- Vim motion tests (LAB-236) ---

func TestLineStart(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"  hello world", "second line", "third line"}
	cm := New(emu, 40, 3, 0)

	// Move cursor to column 8, then press 0.
	for i := 0; i < 8; i++ {
		cm.HandleInput([]byte{'l'})
	}
	cx, _ := cm.CursorPos()
	if cx != 8 {
		t.Fatalf("setup: cx = %d, want 8", cx)
	}

	action := cm.HandleInput([]byte{'0'})
	if action != ActionRedraw {
		t.Errorf("0 should return ActionRedraw, got %d", action)
	}
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Errorf("after 0: cx = %d, want 0", cx)
	}
}

func TestLineEnd(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "", "  "}
	cm := New(emu, 40, 3, 0)

	// $ should go to last non-space char of "hello world" (col 10).
	action := cm.HandleInput([]byte{'$'})
	if action != ActionRedraw {
		t.Errorf("$ should return ActionRedraw, got %d", action)
	}
	cx, _ := cm.CursorPos()
	if cx != 10 {
		t.Errorf("$ on 'hello world': cx = %d, want 10", cx)
	}

	// $ on empty line → cx=0.
	cm.HandleInput([]byte{'j'})
	cm.HandleInput([]byte{'$'})
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Errorf("$ on empty line: cx = %d, want 0", cx)
	}
}

func TestFirstNonBlank(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"   hello", "\tworld", "noindent"}
	cm := New(emu, 40, 3, 0)

	// ^ on "   hello" → cx=3 (first non-space).
	cm.HandleInput([]byte{'^'})
	cx, _ := cm.CursorPos()
	if cx != 3 {
		t.Errorf("^ on '   hello': cx = %d, want 3", cx)
	}

	// ^ on "\tworld" → cx=1 (first non-whitespace after tab).
	cm.HandleInput([]byte{'j'})
	cm.HandleInput([]byte{'^'})
	cx, _ = cm.CursorPos()
	if cx != 1 {
		t.Errorf("^ on '\\tworld': cx = %d, want 1", cx)
	}

	// ^ on "noindent" → cx=0.
	cm.HandleInput([]byte{'j'})
	cm.HandleInput([]byte{'^'})
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Errorf("^ on 'noindent': cx = %d, want 0", cx)
	}
}

func TestFullPageScroll(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(80, 10)
	for i := 0; i < 50; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10, 5)

	// Ctrl-b (0x02) → full page up.
	cm.HandleInput([]byte{0x02})
	if cm.ScrollOffset() != 10 {
		t.Errorf("Ctrl-b: oy = %d, want 10", cm.ScrollOffset())
	}

	// Ctrl-f (0x06) → full page down.
	cm.HandleInput([]byte{0x06})
	if cm.ScrollOffset() != 0 {
		t.Errorf("Ctrl-f: oy = %d, want 0", cm.ScrollOffset())
	}

	// Ctrl-b from bottom, then Ctrl-f to clamp at 0.
	cm.HandleInput([]byte{0x02})
	cm.HandleInput([]byte{0x06})
	cm.HandleInput([]byte{0x06}) // extra — should clamp at 0
	if cm.ScrollOffset() != 0 {
		t.Errorf("Ctrl-f clamp: oy = %d, want 0", cm.ScrollOffset())
	}
}

func TestWordForward(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world foo", "bar baz", "end"}
	cm := New(emu, 40, 3, 0)

	// W from col 0 ("hello") → col 6 ("world").
	cm.HandleInput([]byte{'W'})
	cx, _ := cm.CursorPos()
	if cx != 6 {
		t.Errorf("W #1: cx = %d, want 6", cx)
	}

	// W from col 6 ("world") → col 12 ("foo").
	cm.HandleInput([]byte{'W'})
	cx, _ = cm.CursorPos()
	if cx != 12 {
		t.Errorf("W #2: cx = %d, want 12", cx)
	}
}

func TestWordForwardWrap(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello", "world foo", "end"}
	cm := New(emu, 40, 3, 0)

	// W past "hello" → wrap to "world" on next line (cx=0, cy=1).
	cm.HandleInput([]byte{'W'})
	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 1 {
		t.Errorf("W wrap: cx=%d cy=%d, want cx=0 cy=1", cx, cy)
	}
}

func TestWordForwardAtEnd(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 2)
	emu.screen = []string{"hello", "world"}
	cm := New(emu, 40, 2, 1) // cursor on last line

	// Move to "world" (col 0 of last line).
	// W should be a no-op — no more words.
	action := cm.HandleInput([]byte{'W'})
	cx, cy := cm.CursorPos()
	// Either no-op (ActionNone) or moved to end — just verify no crash.
	_ = action
	_ = cx
	_ = cy
}

func TestWordBackward(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world foo", "bar baz", "end"}
	cm := New(emu, 40, 3, 0)

	// Position at "foo" (col 12).
	for i := 0; i < 12; i++ {
		cm.HandleInput([]byte{'l'})
	}

	// B from "foo" → "world" (col 6).
	cm.HandleInput([]byte{'B'})
	cx, _ := cm.CursorPos()
	if cx != 6 {
		t.Errorf("B #1: cx = %d, want 6", cx)
	}

	// B from "world" → "hello" (col 0).
	cm.HandleInput([]byte{'B'})
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Errorf("B #2: cx = %d, want 0", cx)
	}
}

func TestWordBackwardWrap(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "foo", "end"}
	cm := New(emu, 40, 3, 1) // cursor on "foo" line

	// B from start of "foo" → wraps to "world" on prev line.
	cm.HandleInput([]byte{'B'})
	cx, cy := cm.CursorPos()
	if cx != 6 || cy != 0 {
		t.Errorf("B wrap: cx=%d cy=%d, want cx=6 cy=0", cx, cy)
	}
}

func TestWordEnd(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world foo", "bar", "end"}
	cm := New(emu, 40, 3, 0)

	// E from col 0 → end of "hello" (col 4).
	cm.HandleInput([]byte{'E'})
	cx, _ := cm.CursorPos()
	if cx != 4 {
		t.Errorf("E #1: cx = %d, want 4", cx)
	}

	// E again → end of "world" (col 10).
	cm.HandleInput([]byte{'E'})
	cx, _ = cm.CursorPos()
	if cx != 10 {
		t.Errorf("E #2: cx = %d, want 10", cx)
	}
}

func TestCharSearchForward(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// f + 'o' → jump to first 'o' (col 4 in "hello").
	action := cm.HandleInput([]byte{'f', 'o'})
	if action != ActionRedraw {
		t.Errorf("f+o should return ActionRedraw, got %d", action)
	}
	cx, _ := cm.CursorPos()
	if cx != 4 {
		t.Errorf("f+o: cx = %d, want 4", cx)
	}
}

func TestCharSearchBackward(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// Move to col 8, then F + 'l' → find 'l' backward.
	for i := 0; i < 8; i++ {
		cm.HandleInput([]byte{'l'})
	}
	cm.HandleInput([]byte{'F', 'l'})
	cx, _ := cm.CursorPos()
	if cx != 3 {
		t.Errorf("F+l: cx = %d, want 3", cx)
	}
}

func TestCharSearchTill(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// t + 'w' → land one before 'w' (col 5 in "hello world").
	cm.HandleInput([]byte{'t', 'w'})
	cx, _ := cm.CursorPos()
	if cx != 5 {
		t.Errorf("t+w: cx = %d, want 5", cx)
	}
}

func TestCharSearchTillBackward(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// Move to col 8, then T + 'l' → land one after 'l' (col 4).
	for i := 0; i < 8; i++ {
		cm.HandleInput([]byte{'l'})
	}
	cm.HandleInput([]byte{'T', 'l'})
	cx, _ := cm.CursorPos()
	if cx != 4 {
		t.Errorf("T+l: cx = %d, want 4", cx)
	}
}

func TestCharSearchNoMatch(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// f + 'z' — no 'z' on line → ActionNone, cursor stays.
	action := cm.HandleInput([]byte{'f', 'z'})
	if action != ActionNone {
		t.Errorf("f+z (no match) should return ActionNone, got %d", action)
	}
	cx, _ := cm.CursorPos()
	if cx != 0 {
		t.Errorf("f+z: cx = %d, want 0 (unchanged)", cx)
	}
}

func TestCharSearchRepeat(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"aXbXcXd", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// f + 'X' → first X (col 1).
	cm.HandleInput([]byte{'f', 'X'})
	cx, _ := cm.CursorPos()
	if cx != 1 {
		t.Errorf("f+X: cx = %d, want 1", cx)
	}

	// ; → next X (col 3).
	cm.HandleInput([]byte{';'})
	cx, _ = cm.CursorPos()
	if cx != 3 {
		t.Errorf(";: cx = %d, want 3", cx)
	}

	// , → reverse (F direction) → back to X at col 1.
	cm.HandleInput([]byte{','})
	cx, _ = cm.CursorPos()
	if cx != 1 {
		t.Errorf(",: cx = %d, want 1", cx)
	}

	// ; after , should go FORWARD again (original direction preserved).
	cm.HandleInput([]byte{';'})
	cx, _ = cm.CursorPos()
	if cx != 3 {
		t.Errorf("; after ,: cx = %d, want 3", cx)
	}
}

func TestCharSearchEscapeCancel(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// Move to col 3, press f then Escape — should cancel, cursor unchanged.
	for i := 0; i < 3; i++ {
		cm.HandleInput([]byte{'l'})
	}
	action := cm.HandleInput([]byte{'f', 0x1b})
	cx, _ := cm.CursorPos()
	if cx != 3 {
		t.Errorf("f+Esc: cx = %d, want 3 (unchanged)", cx)
	}
	// Should NOT exit copy mode (Escape consumed by pending cancel).
	if action == ActionExit {
		t.Error("f+Esc should NOT exit copy mode")
	}
}

func TestCharSearchWithSelection(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// Start selection at col 0, then f+'w' → selection extends to col 5.
	cm.HandleInput([]byte{'v'})
	cm.HandleInput([]byte{'f', 'w'})

	text := cm.SelectedText()
	if text != "hello w" {
		t.Errorf("selection after v+fw = %q, want %q", text, "hello w")
	}
}

func TestBatchedCharSearch(t *testing.T) {
	t.Parallel()
	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello world", "test", "end"}
	cm := New(emu, 40, 3, 0)

	// Send 'f' and 'o' in a single HandleInput call.
	action := cm.HandleInput([]byte{'f', 'o'})
	if action != ActionRedraw {
		t.Errorf("batched f+o should return ActionRedraw, got %d", action)
	}
	cx, _ := cm.CursorPos()
	if cx != 4 {
		t.Errorf("batched f+o: cx = %d, want 4", cx)
	}
}

func TestJAtBottomOfLiveView(t *testing.T) {
	emu := newFakeEmulator(80, 10)
	// No scrollback — j at bottom with oy=0 should be a no-op
	cm := New(emu, 80, 10, 9) // cursor at bottom

	action := cm.HandleInput([]byte{'j'})
	if action != ActionNone {
		t.Errorf("j at absolute bottom should return ActionNone, got %d", action)
	}
}
