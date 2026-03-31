package copymode

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/proto"
)

// fakeEmulator implements TerminalEmulator for testing.
type fakeEmulator struct {
	width, height int
	screen        []string // current screen lines (plain text)
	scrollback    []string // scrollback lines (0=oldest)
	screenCells   map[[2]int]proto.Cell
	scrollCells   map[[2]int]proto.Cell
}

func newFakeEmulator(w, h int) *fakeEmulator {
	screen := make([]string, h)
	for i := range screen {
		screen[i] = ""
	}
	return &fakeEmulator{
		width:       w,
		height:      h,
		screen:      screen,
		screenCells: make(map[[2]int]proto.Cell),
		scrollCells: make(map[[2]int]proto.Cell),
	}
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

func (e *fakeEmulator) ScreenCellAt(col, row int) proto.Cell {
	if row < 0 || row >= len(e.screen) {
		return proto.Cell{Char: " ", Width: 1}
	}
	if cell, ok := e.screenCells[[2]int{col, row}]; ok {
		return cell
	}
	return plainTextCell(e.screen[row], col)
}

func (e *fakeEmulator) ScrollbackCellAt(col, row int) proto.Cell {
	if row < 0 || row >= len(e.scrollback) {
		return proto.Cell{Char: " ", Width: 1}
	}
	if cell, ok := e.scrollCells[[2]int{col, row}]; ok {
		return cell
	}
	return plainTextCell(e.scrollback[row], col)
}

func plainTextCell(line string, col int) proto.Cell {
	runes := []rune(line)
	if col < 0 || col >= len(runes) {
		return proto.Cell{Char: " ", Width: 1}
	}
	return proto.Cell{Char: string(runes[col]), Width: 1}
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
	cm2.StartSelection()
	if action := cm2.HandleInput([]byte{0x1b}); action != ActionRedraw {
		t.Errorf("Escape should clear selection, got %d", action)
	}
	if cm2.selecting {
		t.Fatal("Escape should clear selection without exiting copy mode")
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

func TestSearchBackward(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(80, 4)
	emu.scrollback = []string{"hello old", "middle"}
	emu.screen = []string{"hello new", "tail", "final", "bottom"}
	cm := New(emu, 80, 4, 2)

	cm.HandleInput([]byte{'?'})
	cm.HandleInput([]byte("hello"))
	cm.HandleInput([]byte{'\r'})

	if got := cm.SearchQuery(); got != "hello" {
		t.Fatalf("query = %q, want hello", got)
	}
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 2 {
		t.Fatalf("backward search cursor = (%d,%d), want (0,2)", cx, cy)
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

func TestStarAndHashSearchWordUnderCursor(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 2)
	emu.scrollback = []string{"alpha beta alpha"}
	emu.screen = []string{"beta alpha", "tail"}
	cm := New(emu, 40, 2, 0)
	cm.cx = 5 // on "alpha"

	cm.HandleInput([]byte{'*'})
	if got := cm.SearchQuery(); got != "alpha" {
		t.Fatalf("* query = %q, want alpha", got)
	}

	cm.HandleInput([]byte{'#'})
	if got := cm.SearchQuery(); got != "alpha" {
		t.Fatalf("# query = %q, want alpha", got)
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

func TestSelectedTextReversedMultiLineSelection(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha", "bravo", "charlie"}
	cm := New(emu, 20, 3, 0)

	cm.selecting = true
	cm.selStartX = 3
	cm.selStartY = 2
	cm.selEndX = 1
	cm.selEndY = 1

	if got := cm.SelectedText(); got != "ravo\nchar" {
		t.Errorf("SelectedText() = %q, want %q", got, "ravo\nchar")
	}
}

func TestSelectedTextReversedLineSelection(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha", "bravo", "charlie"}
	cm := New(emu, 20, 3, 0)

	cm.selecting = true
	cm.lineSelect = true
	cm.selStartX = 10
	cm.selStartY = 2
	cm.selEndX = 1
	cm.selEndY = 1

	if got := cm.SelectedText(); got != "bravo\ncharlie\n" {
		t.Errorf("SelectedText() = %q, want %q", got, "bravo\ncharlie\n")
	}
}

func TestLineSelectToggleOff(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello", "world", "test"}
	cm := New(emu, 20, 3, 0)

	// V always re-enters tmux line-selection mode.
	cm.HandleInput([]byte{'V'})
	if !cm.selecting {
		t.Fatal("expected selecting=true after V")
	}
	cm.HandleInput([]byte{'j'})
	cm.HandleInput([]byte{'V'})
	if !cm.selecting || !cm.lineSelect {
		t.Fatal("expected line selection to remain active after second V")
	}
	if cm.selStartY != cm.cursorAbsLine() || cm.selEndY != cm.cursorAbsLine() {
		t.Fatal("second V should restart line selection at the current line")
	}
}

func TestVClearsLineSelect(t *testing.T) {
	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello", "world", "test"}
	cm := New(emu, 20, 3, 0)

	// V then v should switch to rectangle selection.
	cm.HandleInput([]byte{'V'})
	if !cm.lineSelect {
		t.Fatal("expected lineSelect=true after V")
	}
	cm.HandleInput([]byte{'v'})
	if cm.lineSelect {
		t.Fatal("expected lineSelect=false after v")
	}
	if !cm.selecting || !cm.rectSelect {
		t.Fatal("expected v to enable rectangle selection")
	}
}

func TestRepeatCountMovesCursor(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 4)
	emu.screen = []string{"one", "two", "three", "four"}
	cm := New(emu, 20, 4, 0)

	action := cm.HandleInput([]byte{'3', 'j'})
	if action != ActionRedraw {
		t.Fatalf("3j should redraw, got %d", action)
	}
	if _, cy := cm.CursorPos(); cy != 3 {
		t.Fatalf("3j moved to row %d, want 3", cy)
	}
}

func TestArrowKeyAliases(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha", "bravo", "charlie"}
	cm := New(emu, 20, 3, 1)

	if action := cm.HandleInput([]byte("\x1b[A")); action != ActionRedraw {
		t.Fatalf("Up should redraw, got %d", action)
	}
	if _, cy := cm.CursorPos(); cy != 0 {
		t.Fatalf("Up moved to row %d, want 0", cy)
	}
	if action := cm.HandleInput([]byte("\x1b[C")); action != ActionRedraw {
		t.Fatalf("Right should redraw, got %d", action)
	}
	if cx, _ := cm.CursorPos(); cx != 1 {
		t.Fatalf("Right moved to col %d, want 1", cx)
	}
}

func TestRectangleSelectionText(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha", "bravo", "charlie"}
	cm := New(emu, 20, 3, 0)

	cm.HandleInput([]byte{'l', 'v', 'j', 'j', 'l', 'l'})
	if got := cm.SelectedText(); got != "lph\nrav\nhar" {
		t.Fatalf("rectangle selection = %q, want %q", got, "lph\nrav\nhar")
	}
}

func TestOtherEndSwapsSelectionEndpoint(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 2)
	emu.screen = []string{"hello world", "second line"}
	cm := New(emu, 20, 2, 0)

	cm.HandleInput([]byte{' ', 'l', 'l', 'l'})
	if cx, _ := cm.CursorPos(); cx != 3 {
		t.Fatalf("setup cursor = %d, want 3", cx)
	}
	cm.HandleInput([]byte{'o'})
	if cx, _ := cm.CursorPos(); cx != 0 {
		t.Fatalf("o should jump to original start, got col %d", cx)
	}
}

func TestCopyCommandsQueuePayload(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 1)
	emu.screen = []string{"hello world"}
	cm := New(emu, 20, 1, 0)

	cm.HandleInput([]byte{'l', 'l', 'D'})
	text, appendCopy := cm.ConsumeCopyText()
	if text != "llo world" || appendCopy {
		t.Fatalf("D queued (%q,%v), want (%q,false)", text, appendCopy, "llo world")
	}

	cm.StartSelection()
	cm.HandleInput([]byte{'l', 'A'})
	text, appendCopy = cm.ConsumeCopyText()
	if text != "ll" || !appendCopy {
		t.Fatalf("A queued (%q,%v), want (%q,true)", text, appendCopy, "ll")
	}
}

func TestMarkJump(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha", "bravo", "charlie"}
	cm := New(emu, 20, 3, 0)

	cm.HandleInput([]byte{'l', 'l', 'X', 'j', '\x1b', 'l'})
	if cx, cy := cm.CursorPos(); cx != 3 || cy != 1 {
		t.Fatalf("setup cursor = (%d,%d), want (3,1)", cx, cy)
	}
	cm.HandleInput([]byte{0x1b, 'x'})
	if cx, cy := cm.CursorPos(); cx != 2 || cy != 0 {
		t.Fatalf("M-x cursor = (%d,%d), want (2,0)", cx, cy)
	}
}

func TestTogglePositionIndicator(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	for i := 0; i < 10; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 20, 3, 0)

	if got := cm.SearchBarText(); got != "" {
		t.Fatalf("initial SearchBarText = %q, want empty", got)
	}
	cm.HandleInput([]byte{'P'})
	if got := cm.SearchBarText(); got == "" {
		t.Fatal("P should expose a position indicator in the status text")
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

func TestSearchMatchNavigationWraps(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.scrollback = []string{"foo old"}
	emu.screen = []string{"middle", "foo new", "tail foo"}
	cm := New(emu, 20, 3, 0)

	cm.searchQuery = "foo"
	cm.runSearch()

	if len(cm.matches) != 3 {
		t.Fatalf("len(matches) = %d, want 3", len(cm.matches))
	}
	if cm.matchIdx != 1 {
		t.Fatalf("initial matchIdx = %d, want 1", cm.matchIdx)
	}

	cm.nextMatch()
	if cm.matchIdx != 2 {
		t.Fatalf("after nextMatch: matchIdx = %d, want 2", cm.matchIdx)
	}

	cm.nextMatch()
	if cm.matchIdx != 0 {
		t.Fatalf("nextMatch should wrap to 0, got %d", cm.matchIdx)
	}

	cm.prevMatch()
	if cm.matchIdx != 2 {
		t.Fatalf("prevMatch should wrap to last match, got %d", cm.matchIdx)
	}
	cx, cy := cm.CursorPos()
	if cx != 5 || cy != 2 {
		t.Fatalf("cursor after wrapped prevMatch = (%d,%d), want (5,2)", cx, cy)
	}
}

func TestViewportHelpers(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.scrollback = []string{"old-0", "old-1", "old-2"}
	emu.screen = []string{"cur-0", "cur-1", "cur-2"}
	cm := New(emu, 20, 3, 0)
	cm.oy = 2

	if got := cm.ViewportHeight(); got != 3 {
		t.Fatalf("ViewportHeight() = %d, want 3", got)
	}
	if got := cm.FirstVisibleLine(); got != 1 {
		t.Fatalf("FirstVisibleLine() = %d, want 1", got)
	}
	if got := cm.LineText(1); got != "old-1" {
		t.Fatalf("LineText(1) = %q, want %q", got, "old-1")
	}
	if got := cm.LineText(4); got != "cur-1" {
		t.Fatalf("LineText(4) = %q, want %q", got, "cur-1")
	}
}

func TestViewportCellAt_PreservesForegroundStyle(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(10, 3)
	emu.screen = []string{"hello", "world", "test!"}
	red := ansi.BasicColor(1)
	emu.screenCells[[2]int{2, 0}] = proto.Cell{
		Char:  "l",
		Width: 1,
		Style: uv.Style{Fg: red},
	}

	cm := New(emu, 10, 3, 0)
	cm.cx = 2
	cm.HandleInput([]byte{'v', 'l'})

	cell := cm.ViewportCellAt(2, 0)
	if cell.Style.Fg == nil {
		t.Fatal("selected cell should preserve its foreground color")
	}
	assertSameColor(t, cell.Style.Fg, red)
}

func TestViewportCellAt_PreservesScrollbackStyle(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(10, 2)
	emu.scrollback = []string{"older"}
	emu.screen = []string{"live", "tail"}
	blue := ansi.BasicColor(4)
	emu.scrollCells[[2]int{1, 0}] = proto.Cell{
		Char:  "l",
		Width: 1,
		Style: uv.Style{Fg: blue},
	}

	cm := New(emu, 10, 2, 1)
	cm.HandleInput([]byte{'g'})

	cell := cm.ViewportCellAt(1, 0)
	if cell.Style.Fg == nil {
		t.Fatal("scrollback cell should preserve its foreground color")
	}
	assertSameColor(t, cell.Style.Fg, blue)
}

func TestViewportCellAt_NormalizesEmptyCharAndNegativeWidth(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(10, 1)
	emu.screen = []string{"x"}
	emu.screenCells[[2]int{0, 0}] = proto.Cell{Char: "", Width: -1}

	cm := New(emu, 10, 1, 0)
	cell := cm.ViewportCellAt(0, 0)
	if cell.Char != " " {
		t.Fatalf("ViewportCellAt(0, 0).Char = %q, want space", cell.Char)
	}
	if cell.Width != 1 {
		t.Fatalf("ViewportCellAt(0, 0).Width = %d, want 1", cell.Width)
	}
}

func assertSameColor(t *testing.T, got, want interface{ RGBA() (r, g, b, a uint32) }) {
	t.Helper()
	gotR, gotG, gotB, gotA := got.RGBA()
	wantR, wantG, wantB, wantA := want.RGBA()
	if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
		t.Fatalf("color = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
			gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
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

	// Cursor is on "world" (col 0 of last line).
	// W should be a no-op — no more words after this one.
	action := cm.HandleInput([]byte{'W'})
	if action != ActionNone {
		t.Errorf("W at last word should return ActionNone, got %d", action)
	}
	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 1 {
		t.Errorf("W at last word: cx=%d cy=%d, want cx=0 cy=1 (unchanged)", cx, cy)
	}
}

func TestViWordForwardTreatsPunctuationAsSeparateWord(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 1)
	emu.screen = []string{"alpha... beta"}
	cm := New(emu, 40, 1, 0)

	cm.HandleInput([]byte{'w'})
	cx, _ := cm.CursorPos()
	if cx != 5 {
		t.Fatalf("w from alpha: cx = %d, want 5", cx)
	}

	cm.HandleInput([]byte{'w'})
	cx, _ = cm.CursorPos()
	if cx != 9 {
		t.Fatalf("w from punctuation: cx = %d, want 9", cx)
	}
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

func TestViWordBackwardTreatsPunctuationAsSeparateWord(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 1)
	emu.screen = []string{"alpha... beta"}
	cm := New(emu, 40, 1, 0)

	for i := 0; i < 9; i++ {
		cm.HandleInput([]byte{'l'})
	}

	cm.HandleInput([]byte{'b'})
	cx, _ := cm.CursorPos()
	if cx != 5 {
		t.Fatalf("b from beta: cx = %d, want 5", cx)
	}

	cm.HandleInput([]byte{'b'})
	cx, _ = cm.CursorPos()
	if cx != 0 {
		t.Fatalf("b from punctuation: cx = %d, want 0", cx)
	}
}

func TestWordBackwardAtAbsoluteTop(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 2)
	emu.screen = []string{"hello", "world"}
	cm := New(emu, 40, 2, 0)

	action := cm.HandleInput([]byte{'B'})
	if action != ActionRedraw {
		t.Fatalf("B at absolute top should return ActionRedraw, got %d", action)
	}
	cx, cy := cm.CursorPos()
	if cx != 0 || cy != 0 {
		t.Fatalf("B at absolute top moved cursor to (%d,%d), want (0,0)", cx, cy)
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

func TestViWordEndTreatsPunctuationAsSeparateWord(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 1)
	emu.screen = []string{"alpha... beta"}
	cm := New(emu, 40, 1, 0)

	cm.HandleInput([]byte{'e'})
	cx, _ := cm.CursorPos()
	if cx != 4 {
		t.Fatalf("e on alpha: cx = %d, want 4", cx)
	}

	cm.HandleInput([]byte{'e'})
	cx, _ = cm.CursorPos()
	if cx != 7 {
		t.Fatalf("e on punctuation: cx = %d, want 7", cx)
	}

	cm.HandleInput([]byte{'e'})
	cx, _ = cm.CursorPos()
	if cx != 12 {
		t.Fatalf("e on beta: cx = %d, want 12", cx)
	}
}

func TestWordEndWrapsToNextLine(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 3)
	emu.screen = []string{"hello", "world foo", "end"}
	cm := New(emu, 40, 3, 0)

	for i := 0; i < 4; i++ {
		cm.HandleInput([]byte{'l'})
	}

	action := cm.HandleInput([]byte{'E'})
	if action != ActionRedraw {
		t.Fatalf("E wrap should return ActionRedraw, got %d", action)
	}
	cx, cy := cm.CursorPos()
	if cx != 4 || cy != 1 {
		t.Fatalf("E wrap moved cursor to (%d,%d), want (4,1)", cx, cy)
	}
}

func TestWordEndWhitespaceAtBottom(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 1)
	emu.screen = []string{"   "}
	cm := New(emu, 40, 1, 0)

	action := cm.HandleInput([]byte{'E'})
	if action != ActionRedraw {
		t.Fatalf("E on trailing whitespace should return ActionRedraw, got %d", action)
	}
	cx, cy := cm.CursorPos()
	if cx != 2 || cy != 0 {
		t.Fatalf("E on trailing whitespace moved cursor to (%d,%d), want (2,0)", cx, cy)
	}
}

func TestRunWordMotion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        byte
		width      int
		height     int
		cursorRow  int
		screen     []string
		scrollback []string
		before     func(*CopyMode)
		wantAction Action
		wantCX     int
		wantCY     int
		wantOY     int
	}{
		{
			name:       "WORD forward wraps to next line",
			key:        'W',
			width:      40,
			height:     2,
			screen:     []string{"hello", "world foo"},
			wantAction: ActionRedraw,
			wantCX:     0,
			wantCY:     1,
			wantOY:     0,
		},
		{
			name:       "vi word forward splits punctuation",
			key:        'w',
			width:      40,
			height:     1,
			screen:     []string{"alpha... beta"},
			wantAction: ActionRedraw,
			wantCX:     5,
			wantCY:     0,
			wantOY:     0,
		},
		{
			name:       "WORD backward sticks to absolute top",
			key:        'B',
			width:      40,
			height:     2,
			screen:     []string{"hello", "world"},
			wantAction: ActionRedraw,
			wantCX:     0,
			wantCY:     0,
			wantOY:     0,
		},
		{
			name:       "vi word backward restores scroll position on failure",
			key:        'b',
			width:      20,
			height:     1,
			screen:     []string{"world"},
			scrollback: []string{"hello"},
			before: func(cm *CopyMode) {
				cm.oy = 1
			},
			wantAction: ActionNone,
			wantCX:     0,
			wantCY:     0,
			wantOY:     1,
		},
		{
			name:       "WORD end keeps last column when trailing whitespace exhausts buffer",
			key:        'E',
			width:      20,
			height:     1,
			screen:     []string{"   "},
			wantAction: ActionRedraw,
			wantCX:     2,
			wantCY:     0,
			wantOY:     0,
		},
		{
			name:   "vi word end wraps to next line",
			key:    'e',
			width:  20,
			height: 2,
			screen: []string{"ab", "cd ef"},
			before: func(cm *CopyMode) {
				cm.cx = 1
			},
			wantAction: ActionRedraw,
			wantCX:     1,
			wantCY:     1,
			wantOY:     0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			emu := newFakeEmulator(tt.width, tt.height)
			emu.screen = append([]string(nil), tt.screen...)
			emu.scrollback = append([]string(nil), tt.scrollback...)
			cm := New(emu, tt.width, tt.height, tt.cursorRow)
			if tt.before != nil {
				tt.before(cm)
			}

			action := cm.runWordMotion(tt.key)
			if action != tt.wantAction {
				t.Fatalf("runWordMotion(%q) action = %d, want %d", tt.key, action, tt.wantAction)
			}
			if got := cm.ScrollOffset(); got != tt.wantOY {
				t.Fatalf("runWordMotion(%q) oy = %d, want %d", tt.key, got, tt.wantOY)
			}
			if cx, cy := cm.CursorPos(); cx != tt.wantCX || cy != tt.wantCY {
				t.Fatalf("runWordMotion(%q) moved cursor to (%d,%d), want (%d,%d)", tt.key, cx, cy, tt.wantCX, tt.wantCY)
			}
		})
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

func TestWheelScrollDoesNotMoveCursor(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(80, 10)
	for i := 0; i < 30; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10, 4)

	cxBefore, cyBefore := cm.CursorPos()
	action := cm.WheelScrollUp(5)
	if action != ActionRedraw {
		t.Fatalf("WheelScrollUp should redraw, got %d", action)
	}
	if cm.ScrollOffset() != 5 {
		t.Fatalf("ScrollOffset after WheelScrollUp = %d, want 5", cm.ScrollOffset())
	}
	cxAfter, cyAfter := cm.CursorPos()
	if cxAfter != cxBefore || cyAfter != cyBefore {
		t.Fatalf("cursor moved during wheel scroll: before=(%d,%d) after=(%d,%d)", cxBefore, cyBefore, cxAfter, cyAfter)
	}
}

func TestWheelScrollExitAtBottom(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(80, 10)
	for i := 0; i < 30; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	cm := New(emu, 80, 10, 4)
	cm.SetScrollExit(true)

	cm.WheelScrollUp(6)
	if action := cm.WheelScrollDown(5); action != ActionRedraw {
		t.Fatalf("WheelScrollDown before bottom = %d, want ActionRedraw", action)
	}
	if action := cm.WheelScrollDown(5); action != ActionExit {
		t.Fatalf("WheelScrollDown at bottom = %d, want ActionExit", action)
	}
}

func TestScrollExitClearedByNonScrollKey(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(80, 10)
	cm := New(emu, 80, 10, 4)
	cm.SetScrollExit(true)

	cm.HandleInput([]byte{'/'})
	if cm.ScrollExit() {
		t.Fatal("search key should clear scroll-exit")
	}

	cm = New(emu, 80, 10, 4)
	cm.SetScrollExit(true)
	cm.HandleInput([]byte{'j'})
	if !cm.ScrollExit() {
		t.Fatal("scroll key should preserve scroll-exit")
	}
}

func TestDecodeCopyModeKeySequences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        []byte
		wantKey      int
		wantConsumed int
	}{
		{name: "literal", input: []byte{'a'}, wantKey: 'a', wantConsumed: 1},
		{name: "escape", input: []byte{0x1b}, wantKey: 0x1b, wantConsumed: 1},
		{name: "alt x", input: []byte{0x1b, 'x'}, wantKey: keyAltX, wantConsumed: 2},
		{name: "alt upper x", input: []byte{0x1b, 'X'}, wantKey: keyAltX, wantConsumed: 2},
		{name: "alt other", input: []byte{0x1b, 'z'}, wantKey: 0x1b, wantConsumed: 1},
		{name: "up", input: []byte("\x1b[A"), wantKey: keyUp, wantConsumed: 3},
		{name: "home", input: []byte("\x1b[H"), wantKey: keyHome, wantConsumed: 3},
		{name: "end", input: []byte("\x1b[F"), wantKey: keyEnd, wantConsumed: 3},
		{name: "page up", input: []byte("\x1b[5~"), wantKey: keyPageUp, wantConsumed: 4},
		{name: "page down", input: []byte("\x1b[6~"), wantKey: keyPageDown, wantConsumed: 4},
		{name: "ctrl up", input: []byte("\x1b[1;5A"), wantKey: keyCtrlUp, wantConsumed: 6},
		{name: "ctrl down short", input: []byte("\x1b[5B"), wantKey: keyCtrlDown, wantConsumed: 4},
		{name: "ss3 home", input: []byte("\x1bOH"), wantKey: keyHome, wantConsumed: 3},
		{name: "incomplete csi", input: []byte("\x1b["), wantKey: 0x1b, wantConsumed: 1},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotKey, gotConsumed := decodeCopyModeKey(tt.input)
			if gotKey != tt.wantKey || gotConsumed != tt.wantConsumed {
				t.Fatalf("decodeCopyModeKey(%q) = (%d,%d), want (%d,%d)", tt.input, gotKey, gotConsumed, tt.wantKey, tt.wantConsumed)
			}
		})
	}
}

func TestPromptModesStatusAndGotoLine(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 5)
	for i := 0; i < 10; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	emu.screen = []string{"screen-0", "screen-1", "screen-2", "screen-3", "screen-4"}
	cm := New(emu, 20, 5, 0)

	if action := cm.HandleInput([]byte{'2', '3'}); action != ActionRedraw {
		t.Fatalf("count input should redraw, got %d", action)
	}
	if got := cm.SearchBarText(); got != "23" {
		t.Fatalf("SearchBarText after count = %q, want %q", got, "23")
	}

	cm.HandleInput([]byte{'?'})
	cm.HandleInput([]byte("alp"))
	if got := cm.SearchBarText(); got != "?alp" {
		t.Fatalf("SearchBarText during backward search = %q, want %q", got, "?alp")
	}

	cm.HandleInput([]byte{0x7f})
	if got := cm.SearchBarText(); got != "?al" {
		t.Fatalf("SearchBarText after backspace = %q, want %q", got, "?al")
	}

	cm.HandleInput([]byte{0x1b})
	if got := cm.SearchBarText(); got != "" {
		t.Fatalf("SearchBarText after cancel = %q, want empty", got)
	}

	cm.HandleInput([]byte{'P'})
	if got := cm.SearchBarText(); !strings.Contains(got, "[0/10]") {
		t.Fatalf("SearchBarText after P = %q, want position indicator", got)
	}

	cm.HandleInput([]byte{':'})
	cm.HandleInput([]byte("12x"))
	if got := cm.SearchBarText(); !strings.Contains(got, ":12") || strings.Contains(got, ":12x") {
		t.Fatalf("goto-line prompt = %q, want numeric prompt only", got)
	}
	cm.HandleInput([]byte{'\r'})
	if got := cm.ScrollOffset(); got != 10 {
		t.Fatalf("goto-line scroll offset = %d, want 10", got)
	}
}

func TestSearchAgainAndMatchCopyWithoutSelection(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 4)
	emu.screen = []string{"alpha one", "middle", "alpha two", "alpha three"}
	cm := New(emu, 20, 4, 0)

	cm.HandleInput([]byte{'/'})
	cm.HandleInput([]byte("alpha"))
	cm.HandleInput([]byte{'\r'})

	if got := cm.SelectedText(); got != "alpha" {
		t.Fatalf("SelectedText on current match = %q, want %q", got, "alpha")
	}
	if action := cm.HandleInput([]byte{'\r'}); action != ActionYank {
		t.Fatalf("Enter on search match = %d, want ActionYank", action)
	}
	text, appendCopy := cm.ConsumeCopyText()
	if text != "alpha" || appendCopy {
		t.Fatalf("ConsumeCopyText() = (%q,%v), want (%q,false)", text, appendCopy, "alpha")
	}

	cm.HandleInput([]byte{'n'})
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 2 {
		t.Fatalf("n moved cursor to (%d,%d), want (0,2)", cx, cy)
	}

	cm.HandleInput([]byte{'N'})
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 0 {
		t.Fatalf("N moved cursor to (%d,%d), want (0,0)", cx, cy)
	}
}

func TestSearchMatchCopyUsesRuneColumns(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(40, 1)
	emu.screen = []string{"  ⏵⏵ bypass permissions"}
	cm := New(emu, 40, 1, 0)

	cm.HandleInput([]byte{'/'})
	cm.HandleInput([]byte("bypass"))
	cm.HandleInput([]byte{'\r'})

	if cx, cy := cm.CursorPos(); cx != 5 || cy != 0 {
		t.Fatalf("CursorPos after search = (%d,%d), want (5,0)", cx, cy)
	}
	if got := cm.SelectedText(); got != "bypass" {
		t.Fatalf("SelectedText on unicode-prefixed match = %q, want %q", got, "bypass")
	}
	if action := cm.HandleInput([]byte{'\r'}); action != ActionYank {
		t.Fatalf("Enter on unicode-prefixed search match = %d, want ActionYank", action)
	}
	text, appendCopy := cm.ConsumeCopyText()
	if text != "bypass" || appendCopy {
		t.Fatalf("ConsumeCopyText() = (%q,%v), want (%q,false)", text, appendCopy, "bypass")
	}
}

func TestSearchWordUnderCursorSkipsWhitespace(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(30, 1)
	emu.screen = []string{"alpha beta alpha"}
	cm := New(emu, 30, 1, 0)
	cm.cx = 5 // on the whitespace before beta

	cm.HandleInput([]byte{'*'})
	if got := cm.SearchQuery(); got != "beta" {
		t.Fatalf("* query = %q, want beta", got)
	}
	if cx, _ := cm.CursorPos(); cx != 6 {
		t.Fatalf("* moved cursor to %d, want 6", cx)
	}

	cm.HandleInput([]byte{'#'})
	if got := cm.SearchQuery(); got != "beta" {
		t.Fatalf("# query = %q, want beta", got)
	}
}

func TestSetCursorAndSelectWord(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 2)
	emu.screen = []string{"alpha... beta", ""}
	cm := New(emu, 20, 2, 0)

	if action := cm.SetCursor(100, 0); action != ActionRedraw {
		t.Fatalf("SetCursor on populated line = %d, want ActionRedraw", action)
	}
	if cx, cy := cm.CursorPos(); cx != 12 || cy != 0 {
		t.Fatalf("SetCursor clamped to (%d,%d), want (12,0)", cx, cy)
	}

	if action := cm.SetCursor(100, 1); action != ActionRedraw {
		t.Fatalf("SetCursor on empty line = %d, want ActionRedraw", action)
	}
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 1 {
		t.Fatalf("SetCursor on empty line = (%d,%d), want (0,1)", cx, cy)
	}

	if action := cm.SetCursor(8, 0); action != ActionRedraw {
		t.Fatalf("SetCursor to whitespace = %d, want ActionRedraw", action)
	}
	if action := cm.SelectWord(); action != ActionRedraw {
		t.Fatalf("SelectWord() = %d, want ActionRedraw", action)
	}
	if got := cm.SelectedText(); got != "beta" {
		t.Fatalf("SelectWord() selected %q, want %q", got, "beta")
	}
	if cx, cy := cm.CursorPos(); cx != 12 || cy != 0 {
		t.Fatalf("SelectWord moved cursor to (%d,%d), want (12,0)", cx, cy)
	}
	if action := cm.SetCursor(12, 0); action != ActionNone {
		t.Fatalf("SetCursor to current position = %d, want ActionNone", action)
	}
}

func TestParagraphMotions(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 6)
	emu.screen = []string{"first", "", "second", "third", "", "tail"}
	cm := New(emu, 20, 6, 0)
	cm.HandleInput([]byte{'j', 'j'})

	if action := cm.HandleInput([]byte{'}'}); action != ActionRedraw {
		t.Fatalf("} = %d, want ActionRedraw", action)
	}
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 4 {
		t.Fatalf("} moved cursor to (%d,%d), want (0,4)", cx, cy)
	}

	if action := cm.HandleInput([]byte{'{'}); action != ActionRedraw {
		t.Fatalf("{ = %d, want ActionRedraw", action)
	}
	if cx, cy := cm.CursorPos(); cx != 0 || cy != 1 {
		t.Fatalf("{ moved cursor to (%d,%d), want (0,1)", cx, cy)
	}
}

func TestMatchingBracketFromScannedCandidate(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(30, 1)
	emu.screen = []string{"xx(a[b{c}d]e)yy"}
	cm := New(emu, 30, 1, 0)

	if action := cm.HandleInput([]byte{'%'}); action != ActionRedraw {
		t.Fatalf("%% from scanned candidate = %d, want ActionRedraw", action)
	}
	if cx, _ := cm.CursorPos(); cx != 12 {
		t.Fatalf("%% moved cursor to %d, want 12", cx)
	}

	if action := cm.HandleInput([]byte{'%'}); action != ActionRedraw {
		t.Fatalf("%% from closing bracket = %d, want ActionRedraw", action)
	}
	if cx, _ := cm.CursorPos(); cx != 2 {
		t.Fatalf("%% moved cursor back to %d, want 2", cx)
	}
}

func TestGotoLineCenterAndCharSearchCount(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 5)
	for i := 0; i < 10; i++ {
		emu.scrollback = append(emu.scrollback, fmt.Sprintf("line-%d", i))
	}
	emu.screen = []string{"aXbXcXd", "screen-1", "screen-2", "screen-3", "screen-4"}
	cm := New(emu, 20, 5, 0)

	cm.HandleInput([]byte{'g'})
	cm.HandleInput([]byte{'3', 'j'})
	if action := cm.HandleInput([]byte{'z'}); action != ActionRedraw {
		t.Fatalf("z = %d, want ActionRedraw", action)
	}
	if _, cy := cm.CursorPos(); cy != 2 {
		t.Fatalf("z centered cursor on row %d, want 2", cy)
	}
	if got := cm.ScrollOffset(); got != 9 {
		t.Fatalf("z scroll offset = %d, want 9", got)
	}

	emu2 := newFakeEmulator(20, 1)
	emu2.screen = []string{"aXbXcXd"}
	cm2 := New(emu2, 20, 1, 0)
	if action := cm2.HandleInput([]byte{'2', 'f', 'X'}); action != ActionRedraw {
		t.Fatalf("2fX = %d, want ActionRedraw", action)
	}
	if cx, cy := cm2.CursorPos(); cx != 3 || cy != 0 {
		t.Fatalf("2fX moved cursor to (%d,%d), want (3,0)", cx, cy)
	}
}

func TestViWordEndWrapsToNextLine(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 2)
	emu.screen = []string{"ab", "cd ef"}
	cm := New(emu, 20, 2, 0)
	cm.cx = 1

	if action := cm.HandleInput([]byte{'e'}); action != ActionRedraw {
		t.Fatalf("e wrap = %d, want ActionRedraw", action)
	}
	if cx, cy := cm.CursorPos(); cx != 1 || cy != 1 {
		t.Fatalf("e wrap moved cursor to (%d,%d), want (1,1)", cx, cy)
	}
}

func TestViewportOverlayIncludesSelectionRangeAndVisibleHighlights(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"hello world", "second line", "third line"}
	cm := New(emu, 20, 3, 0)

	cm.cx = 2
	cm.StartSelection()
	cm.HandleInput([]byte{'j', 'l', 'l'})

	overlay := cm.ViewportOverlay()
	if overlay == nil {
		t.Fatal("ViewportOverlay() = nil, want overlay")
	}
	if overlay.Cursor != (proto.CursorPosition{Col: 4, Row: 1}) {
		t.Fatalf("overlay.Cursor = %+v, want %+v", overlay.Cursor, proto.CursorPosition{Col: 4, Row: 1})
	}
	if overlay.Selection == nil {
		t.Fatal("overlay.Selection = nil, want selection range")
	}
	if overlay.Selection.Mode != proto.SelectionModeCharacter {
		t.Fatalf("overlay.Selection.Mode = %d, want %d", overlay.Selection.Mode, proto.SelectionModeCharacter)
	}
	if overlay.Selection.StartLine != 0 || overlay.Selection.StartCol != 2 || overlay.Selection.EndLine != 1 || overlay.Selection.EndCol != 4 {
		t.Fatalf("overlay.Selection = %+v, want start=(0,2) end=(1,4)", overlay.Selection)
	}

	wantLines := []proto.HighlightLine{
		{Row: 0, Spans: []proto.HighlightSpan{{StartCol: 2, EndCol: 20, Kind: proto.HighlightSelection}}},
		{Row: 1, Spans: []proto.HighlightSpan{{StartCol: 0, EndCol: 5, Kind: proto.HighlightSelection}}},
	}
	if fmt.Sprintf("%#v", overlay.HighlightedLines) != fmt.Sprintf("%#v", wantLines) {
		t.Fatalf("overlay.HighlightedLines = %#v, want %#v", overlay.HighlightedLines, wantLines)
	}
}

func TestViewportOverlayMarksCurrentAndNonCurrentSearchMatches(t *testing.T) {
	t.Parallel()

	emu := newFakeEmulator(20, 3)
	emu.screen = []string{"alpha one", "middle", "alpha two"}
	cm := New(emu, 20, 3, 0)

	cm.HandleInput([]byte{'/'})
	cm.HandleInput([]byte("alpha"))
	cm.HandleInput([]byte{'\r'})

	overlay := cm.ViewportOverlay()
	if overlay == nil {
		t.Fatal("ViewportOverlay() = nil, want overlay")
	}

	wantLines := []proto.HighlightLine{
		{Row: 0, Spans: []proto.HighlightSpan{{StartCol: 0, EndCol: 5, Kind: proto.HighlightCurrentMatch}}},
		{Row: 2, Spans: []proto.HighlightSpan{{StartCol: 0, EndCol: 5, Kind: proto.HighlightSearchMatch}}},
	}
	if fmt.Sprintf("%#v", overlay.HighlightedLines) != fmt.Sprintf("%#v", wantLines) {
		t.Fatalf("overlay.HighlightedLines = %#v, want %#v", overlay.HighlightedLines, wantLines)
	}
}
