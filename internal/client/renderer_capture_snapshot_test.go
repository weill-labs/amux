package client

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

type captureSnapshotFakeEmulator struct {
	width, height       int
	scrollback          []string
	screen              []string
	pushed              uint64
	screenChanged       bool
	lineReads           []int
	screenReads         []int
	cellReads           []screenCellRead
	changedRows         []int
	renderCalls         int
	renderNoCursorCalls int
	hasCursorBlock      bool
	cursorBlockCol      int
	cursorBlockRow      int
	cell                uv.Cell
}

type screenCellRead struct {
	row int
	col int
}

func newCaptureSnapshotFakeEmulator(lines []string, pushed uint64) *captureSnapshotFakeEmulator {
	return &captureSnapshotFakeEmulator{
		width:      4,
		height:     2,
		scrollback: append([]string(nil), lines...),
		screen:     []string{"screen-0", "screen-1"},
		pushed:     pushed,
		cell:       uv.Cell{Content: "x", Width: 1},
	}
}

func (e *captureSnapshotFakeEmulator) Write(data []byte) (int, error) { return len(data), nil }
func (e *captureSnapshotFakeEmulator) DrainScreenChangeRows() []int {
	if e.changedRows != nil {
		rows := e.changedRows
		e.changedRows = nil
		e.screenChanged = false
		return rows
	}
	if !e.screenChanged {
		return nil
	}
	e.screenChanged = false
	rows := make([]int, e.height)
	for row := range rows {
		rows[row] = row
	}
	return rows
}
func (e *captureSnapshotFakeEmulator) DrainScreenChanges() bool {
	return len(e.DrainScreenChangeRows()) != 0
}
func (e *captureSnapshotFakeEmulator) Read([]byte) (int, error) { return 0, io.EOF }
func (e *captureSnapshotFakeEmulator) Close() error             { return nil }
func (e *captureSnapshotFakeEmulator) Render() string {
	e.renderCalls++
	rendered := e.screenANSI()
	if e.hasCursorBlock {
		rendered = fmt.Sprintf("%s|cursor:%d,%d", rendered, e.cursorBlockCol, e.cursorBlockRow)
	}
	return rendered
}
func (e *captureSnapshotFakeEmulator) Resize(width, height int)   { e.width, e.height = width, height }
func (e *captureSnapshotFakeEmulator) Size() (int, int)           { return e.width, e.height }
func (e *captureSnapshotFakeEmulator) Reset()                     {}
func (e *captureSnapshotFakeEmulator) CursorPosition() (int, int) { return 0, 0 }
func (e *captureSnapshotFakeEmulator) CursorPhantom() bool        { return false }
func (e *captureSnapshotFakeEmulator) CursorHidden() bool         { return false }
func (e *captureSnapshotFakeEmulator) TerminalState() mux.TerminalState {
	return mux.TerminalState{}
}
func (e *captureSnapshotFakeEmulator) ScrollbackLen() int { return len(e.scrollback) }
func (e *captureSnapshotFakeEmulator) ScrollbackPushed() uint64 {
	return e.pushed
}
func (e *captureSnapshotFakeEmulator) ScrollbackLineText(y int) string {
	e.lineReads = append(e.lineReads, y)
	if y < 0 || y >= len(e.scrollback) {
		return ""
	}
	return e.scrollback[y]
}
func (e *captureSnapshotFakeEmulator) ScrollbackCellAt(int, int) (uv.Cell, bool) {
	return uv.Cell{}, false
}
func (e *captureSnapshotFakeEmulator) RenderWithoutCursorBlock() string {
	e.renderNoCursorCalls++
	return fmt.Sprintf("%s|cursorless:%d,%d", e.screenANSI(), e.cursorBlockCol, e.cursorBlockRow)
}
func (e *captureSnapshotFakeEmulator) HasCursorBlock() bool { return e.hasCursorBlock }
func (e *captureSnapshotFakeEmulator) CursorBlockPosition() (int, int, bool) {
	return e.cursorBlockCol, e.cursorBlockRow, e.hasCursorBlock
}
func (e *captureSnapshotFakeEmulator) ScreenLineText(y int) string {
	e.screenReads = append(e.screenReads, y)
	if y < 0 || y >= len(e.screen) {
		return ""
	}
	return e.screen[y]
}
func (e *captureSnapshotFakeEmulator) LineWrapped(int) bool       { return false }
func (e *captureSnapshotFakeEmulator) ScreenContains(string) bool { return false }
func (e *captureSnapshotFakeEmulator) CellAt(col, row int) *uv.Cell {
	e.cellReads = append(e.cellReads, screenCellRead{row: row, col: col})
	return &e.cell
}
func (e *captureSnapshotFakeEmulator) IsAltScreen() bool { return false }
func (e *captureSnapshotFakeEmulator) MouseProtocol() mux.MouseProtocol {
	return mux.MouseProtocol{}
}
func (e *captureSnapshotFakeEmulator) EncodeMouse(mouse.Event, int, int) []byte {
	return nil
}

func (e *captureSnapshotFakeEmulator) screenANSI() string {
	return strings.Join(e.screen, "\n")
}

func scrollbackState(lines []string, pushed uint64) paneRenderSnapshotState {
	return paneRenderSnapshotState{
		scrollbackLen:    len(lines),
		scrollbackPushed: pushed,
		scrollback:       paneBufferLines(lines),
	}
}

func paneBufferLines(lines []string) []paneBufferLine {
	out := make([]paneBufferLine, len(lines))
	for i, line := range lines {
		out[i] = paneBufferLine{text: line}
	}
	return out
}

func sameScrollbackBacking(a, b []paneBufferLine) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for i := range b {
		if &a[0] == &b[i] {
			return true
		}
	}
	return false
}

func TestCapturePaneRenderSnapshotIncrementalScrollback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prev       paneRenderSnapshotState
		lines      []string
		pushed     uint64
		wantLines  []string
		wantReads  []int
		wantReuse  bool
		wantPushed uint64
	}{
		{
			name:       "cold start rebuilds all scrollback",
			lines:      []string{"a", "b", "c"},
			pushed:     3,
			wantLines:  []string{"a", "b", "c"},
			wantReads:  []int{0, 1, 2},
			wantPushed: 3,
		},
		{
			name:       "no change reuses previous slice",
			prev:       scrollbackState([]string{"a", "b"}, 2),
			lines:      []string{"a", "b"},
			pushed:     2,
			wantLines:  []string{"a", "b"},
			wantReuse:  true,
			wantPushed: 2,
		},
		{
			name:       "appended rows read only new tail rows",
			prev:       scrollbackState([]string{"a", "b"}, 2),
			lines:      []string{"a", "b", "c", "d"},
			pushed:     4,
			wantLines:  []string{"a", "b", "c", "d"},
			wantReads:  []int{2, 3},
			wantPushed: 4,
		},
		{
			name:       "trimmed rows drop from previous front without reads",
			prev:       scrollbackState([]string{"a", "b", "c", "d"}, 4),
			lines:      []string{"c", "d"},
			pushed:     4,
			wantLines:  []string{"c", "d"},
			wantReads:  nil,
			wantReuse:  true,
			wantPushed: 4,
		},
		{
			name:       "appended and trimmed rows reuse tail then append new rows",
			prev:       scrollbackState([]string{"a", "b", "c"}, 3),
			lines:      []string{"c", "d", "e"},
			pushed:     5,
			wantLines:  []string{"c", "d", "e"},
			wantReads:  []int{1, 2},
			wantPushed: 5,
		},
		{
			name:       "counter mismatch falls back to full rebuild",
			prev:       scrollbackState([]string{"a", "b"}, 4),
			lines:      []string{"x", "y"},
			pushed:     1,
			wantLines:  []string{"x", "y"},
			wantReads:  []int{0, 1},
			wantPushed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			emu := newCaptureSnapshotFakeEmulator(tt.lines, tt.pushed)
			snap, state, _ := capturePaneRenderSnapshot(emu, tt.prev)

			if got := paneRenderSnapshotLines(snap.scrollback); !equalStrings(got, tt.wantLines) {
				t.Fatalf("snapshot scrollback = %#v, want %#v", got, tt.wantLines)
			}
			if got := paneRenderSnapshotLines(state.scrollback); !equalStrings(got, tt.wantLines) {
				t.Fatalf("state scrollback = %#v, want %#v", got, tt.wantLines)
			}
			if !equalInts(emu.lineReads, tt.wantReads) {
				t.Fatalf("ScrollbackLineText reads = %#v, want %#v", emu.lineReads, tt.wantReads)
			}
			if state.scrollbackLen != len(tt.wantLines) {
				t.Fatalf("state scrollbackLen = %d, want %d", state.scrollbackLen, len(tt.wantLines))
			}
			if state.scrollbackPushed != tt.wantPushed {
				t.Fatalf("state scrollbackPushed = %d, want %d", state.scrollbackPushed, tt.wantPushed)
			}
			if tt.wantReuse && !sameScrollbackBacking(snap.scrollback, tt.prev.scrollback) {
				t.Fatal("snapshot did not reuse previous scrollback backing array")
			}
		})
	}
}

func TestCapturePaneRenderSnapshotSkipsRenderedANSIOnPublish(t *testing.T) {
	t.Parallel()

	emu := newCaptureSnapshotFakeEmulator(nil, 0)
	emu.screen = []string{"cached", "screen"}
	emu.screenChanged = true
	emu.hasCursorBlock = true
	emu.cursorBlockCol = 1
	emu.cursorBlockRow = 0

	snap, state, screenChanged := capturePaneRenderSnapshot(emu, paneRenderSnapshotState{})
	if !screenChanged {
		t.Fatal("initial capture should report drained screen changes")
	}
	if !snap.hasCursorBlock {
		t.Fatal("snapshot should record cursor block metadata")
	}
	if snap.cursorBlockCol != 1 || snap.cursorBlockRow != 0 {
		t.Fatalf("cursor block position = %d,%d, want 1,0", snap.cursorBlockCol, snap.cursorBlockRow)
	}
	if got := emu.renderCalls; got != 0 {
		t.Fatalf("initial Render calls = %d, want 0", got)
	}
	if got := emu.renderNoCursorCalls; got != 0 {
		t.Fatalf("initial RenderWithoutCursorBlock calls = %d, want 0", got)
	}

	emu.renderCalls = 0
	emu.renderNoCursorCalls = 0
	emu.screenChanged = false
	_, _, screenChanged = capturePaneRenderSnapshot(emu, state)
	if screenChanged {
		t.Fatal("unchanged capture should not report screen changes")
	}
	if got := emu.renderCalls; got != 0 {
		t.Fatalf("Render calls after unchanged screen = %d, want 0", got)
	}
	if got := emu.renderNoCursorCalls; got != 0 {
		t.Fatalf("RenderWithoutCursorBlock calls after unchanged screen = %d, want 0", got)
	}
}

func TestPaneRenderSnapshotANSIStringUsesCapturedCells(t *testing.T) {
	t.Parallel()

	snap := paneRenderSnapshot{
		width:  4,
		height: 2,
		screen: []paneBufferLine{
			{cells: []render.ScreenCell{
				{Char: "h", Width: 1},
				{Char: "i", Width: 1},
				{Char: " ", Width: 1},
				{Char: " ", Width: 1},
			}},
			{cells: []render.ScreenCell{
				{Char: "l", Width: 1},
				{Char: "i", Width: 1, Link: uv.Link{URL: "https://example.com"}},
				{Char: "n", Width: 1, Link: uv.Link{URL: "https://example.com"}},
				{Char: "k", Width: 1},
			}},
		},
	}

	plainCaps := proto.ClientCapabilities{}
	if got := render.MaterializeGrid(snap.ansiString(plainCaps), snap.width, snap.height); got != "hi\nlink" {
		t.Fatalf("materialized ANSI capture = %q, want %q", got, "hi\nlink")
	}
	if ansi := snap.ansiString(plainCaps); strings.Contains(ansi, "\033]8;") {
		t.Fatalf("ANSI capture should filter hyperlinks without capability, got %q", ansi)
	}

	ansi := snap.ansiString(proto.ClientCapabilities{Hyperlinks: true})
	if !strings.Contains(ansi, "\033]8;") {
		t.Fatalf("ANSI capture should preserve hyperlinks with capability, got %q", ansi)
	}
}

func TestSnapshotPaneDataRenderScreenStripsInactiveCursorBlock(t *testing.T) {
	t.Parallel()

	reverse := uv.Style{Attrs: uv.AttrReverse}
	snap := paneRenderSnapshot{
		width:          2,
		height:         1,
		cursorBlockCol: 0,
		cursorBlockRow: 0,
		hasCursorBlock: true,
		screen: []paneBufferLine{
			{cells: []render.ScreenCell{
				{Char: " ", Width: 1, Style: reverse},
				{Char: "x", Width: 1},
			}},
		},
	}
	pane := snapshotPaneData{pane: snap}

	active := pane.RenderScreen(true)
	if !strings.Contains(active, "\033[7m") {
		t.Fatalf("active pane ANSI should preserve cursor block reverse video, got %q", active)
	}
	inactive := pane.RenderScreen(false)
	if strings.Contains(inactive, "\033[7m") {
		t.Fatalf("inactive pane ANSI should strip cursor block reverse video, got %q", inactive)
	}
}

func TestPublishPaneCaptureMissingEmulatorReportsNoScreenChange(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(20, 4, 100)
	t.Cleanup(r.Close)

	r.withActor(func(st *rendererActorState) {
		if got := r.publishPaneCapture(st, 42); got {
			t.Fatal("missing emulator publish should not report screen changes")
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	return slices.Equal(a, b)
}

func sameScreenCellsBacking(a, b paneBufferLine) bool {
	return len(a.cells) > 0 && len(b.cells) > 0 && &a.cells[0] == &b.cells[0]
}

func screenCellReads(rows []int, width int) []screenCellRead {
	reads := make([]screenCellRead, 0, len(rows)*width)
	for _, row := range rows {
		for col := 0; col < width; col++ {
			reads = append(reads, screenCellRead{row: row, col: col})
		}
	}
	return reads
}

func TestCapturePaneRenderSnapshotIncrementalScreenCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		firstScreen    []string
		secondScreen   []string
		changedRows    []int
		width          int
		height         int
		wantReadRows   []int
		wantCellReads  []screenCellRead
		wantReusedRows []int
		wantChanged    bool
	}{
		{
			name:           "no change reuses previous screen rows",
			firstScreen:    []string{"top", "bottom"},
			secondScreen:   []string{"top", "bottom"},
			width:          4,
			height:         2,
			wantReusedRows: []int{0, 1},
		},
		{
			name:           "partial change rebuilds only touched rows",
			firstScreen:    []string{"top", "bottom"},
			secondScreen:   []string{"top", "changed"},
			changedRows:    []int{1},
			width:          4,
			height:         2,
			wantReadRows:   []int{1},
			wantCellReads:  screenCellReads([]int{1}, 4),
			wantReusedRows: []int{0},
			wantChanged:    true,
		},
		{
			name:          "full change rebuilds all screen rows",
			firstScreen:   []string{"top", "bottom"},
			secondScreen:  []string{"changed-top", "changed-bottom"},
			changedRows:   []int{0, 1},
			width:         4,
			height:        2,
			wantReadRows:  []int{0, 1},
			wantCellReads: screenCellReads([]int{0, 1}, 4),
			wantChanged:   true,
		},
		{
			name:          "shape mismatch falls back to full screen rebuild",
			firstScreen:   []string{"top", "bottom"},
			secondScreen:  []string{"new-top", "new-bottom", "new-tail"},
			width:         5,
			height:        3,
			wantReadRows:  []int{0, 1, 2},
			wantCellReads: screenCellReads([]int{0, 1, 2}, 5),
			wantChanged:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			emu := newCaptureSnapshotFakeEmulator(nil, 0)
			emu.width = 4
			emu.height = 2
			emu.screen = append([]string(nil), tt.firstScreen...)
			emu.changedRows = []int{0, 1}
			first, state, _ := capturePaneRenderSnapshot(emu, paneRenderSnapshotState{})

			emu.width = tt.width
			emu.height = tt.height
			emu.screen = append([]string(nil), tt.secondScreen...)
			emu.changedRows = append([]int(nil), tt.changedRows...)
			emu.screenReads = nil
			emu.cellReads = nil
			second, _, changed := capturePaneRenderSnapshot(emu, state)

			if got := paneRenderSnapshotLines(second.screen); !slices.Equal(got, tt.secondScreen) {
				t.Fatalf("snapshot screen = %#v, want %#v", got, tt.secondScreen)
			}
			if changed != tt.wantChanged {
				t.Fatalf("screenChanged = %v, want %v", changed, tt.wantChanged)
			}
			if !slices.Equal(emu.screenReads, tt.wantReadRows) {
				t.Fatalf("ScreenLineText reads = %#v, want %#v", emu.screenReads, tt.wantReadRows)
			}
			if !slices.Equal(emu.cellReads, tt.wantCellReads) {
				t.Fatalf("CellAt reads = %#v, want %#v", emu.cellReads, tt.wantCellReads)
			}
			for _, row := range tt.wantReusedRows {
				if !sameScreenCellsBacking(second.screen[row], first.screen[row]) {
					t.Fatalf("screen row %d did not reuse previous cells backing array", row)
				}
			}
		})
	}
}

func TestHandleCaptureRequestDoesNotWaitForRendererActor(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(20, 4, 100)
	t.Cleanup(r.Close)

	r.HandleLayout(singlePane20x3())
	r.HandlePaneOutput(1, []byte("ready"))

	actorRelease := make(chan struct{})
	actorStarted := make(chan struct{})
	go r.withActor(func(*rendererActorState) {
		close(actorStarted)
		<-actorRelease
	})

	select {
	case <-actorStarted:
	case <-time.After(time.Second):
		t.Fatal("renderer actor did not start blocking command")
	}

	captureDone := make(chan string, 1)
	go func() {
		resp := r.HandleCaptureRequest([]string{"pane-1"}, nil)
		captureDone <- resp.CmdOutput
	}()

	select {
	case out := <-captureDone:
		if !strings.Contains(out, "ready") {
			t.Fatalf("capture output = %q, want pane content", out)
		}
	case <-time.After(2 * time.Second):
		close(actorRelease)
		out := <-captureDone
		t.Fatalf("capture waited for renderer actor; output after release was %q", out)
	}

	close(actorRelease)
}

func TestCaptureDisplayDoesNotWaitForRendererActor(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(20, 4)
	t.Cleanup(cr.renderer.Close)

	cr.HandleLayout(singlePane20x3())
	cr.HandlePaneOutput(1, []byte("ready"))
	cr.RenderDiff()

	actorRelease := make(chan struct{})
	actorStarted := make(chan struct{})
	go cr.renderer.withActor(func(*rendererActorState) {
		close(actorStarted)
		<-actorRelease
	})

	select {
	case <-actorStarted:
	case <-time.After(time.Second):
		t.Fatal("renderer actor did not start blocking command")
	}

	captureDone := make(chan string, 1)
	go func() {
		var out string
		for i := 0; i < 100; i++ {
			out = cr.CaptureDisplay()
		}
		captureDone <- out
	}()

	select {
	case out := <-captureDone:
		if !strings.Contains(out, "ready") {
			t.Fatalf("display capture output = %q, want pane content", out)
		}
	case <-time.After(200 * time.Millisecond):
		close(actorRelease)
		out := <-captureDone
		t.Fatalf("CaptureDisplay waited for renderer actor; output after release was %q", out)
	}

	close(actorRelease)
}

func TestCaptureDisplaySnapshotEdgeCases(t *testing.T) {
	t.Parallel()

	var zero Renderer
	if got := zero.CaptureDisplay(); got != "" {
		t.Fatalf("zero renderer CaptureDisplay() = %q, want empty", got)
	}
	if got := zero.CaptureDisplayPane(1); got != "" {
		t.Fatalf("zero renderer CaptureDisplayPane() = %q, want empty", got)
	}

	r := NewWithScrollback(20, 4, 100)
	t.Cleanup(r.Close)

	if got := r.CaptureDisplayPane(1); got != "" {
		t.Fatalf("CaptureDisplayPane without layout = %q, want empty", got)
	}

	r.HandleLayout(singlePane20xN(10))
	if got := r.CaptureDisplayPane(99); got != "" {
		t.Fatalf("CaptureDisplayPane for missing pane = %q, want empty", got)
	}
	if got := r.CaptureDisplayPane(1); got != "" {
		t.Fatalf("CaptureDisplayPane without snapshot = %q, want empty", got)
	}

	r.withActor(func(st *rendererActorState) {
		next := *st.snapshot
		next.layout = mux.NewLeafByID(1, 0, 0, 20, 10)
		next.height = 4
		st.snapshot = &next
		r.publishSnapshot(&next)
	})
	if got := r.CaptureDisplayPane(1); got != "" {
		t.Fatalf("clamped CaptureDisplayPane without snapshot = %q, want empty", got)
	}
}

func TestPaneCaptureMissingWarmSnapshotReturnsBlank(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(20, 6, 100)
	t.Cleanup(r.Close)

	r.HandleLayout(singlePane20x5())
	r.withActor(func(st *rendererActorState) {
		next := *st.snapshot
		next.paneCaptures = make(map[uint32]paneRenderSnapshot)
		st.snapshot = &next
		r.publishSnapshot(&next)
	})

	pane, ok := r.loadSnapshot().paneCapture(1)
	if !ok {
		t.Fatal("paneCapture returned no snapshot for existing pane")
	}
	if pane.width != 20 || pane.height != 4 {
		t.Fatalf("pane size = %dx%d, want 20x4", pane.width, pane.height)
	}
	if len(pane.screen) != pane.height {
		t.Fatalf("screen lines = %d, want %d", len(pane.screen), pane.height)
	}
	if got, want := pane.rendered, "\n\n\n"; got != want {
		t.Fatalf("rendered blank pane = %q, want %q", got, want)
	}
	if !pane.cursorHidden {
		t.Fatal("blank pane cursor should be hidden")
	}
}

func TestCapturePaneTextActorFallbackUsesCurrentCapabilities(t *testing.T) {
	t.Parallel()

	r := NewWithScrollback(80, 24, 100)
	t.Cleanup(r.Close)

	r.SetCapabilities(proto.ClientCapabilities{Hyperlinks: true})
	r.HandleLayout(singlePane20x3())
	r.HandlePaneOutput(1, []byte("\033]8;;https://example.com\033\\test-link\033]8;;\033\\"))
	r.withActor(func(st *rendererActorState) {
		next := *st.snapshot
		next.paneCaptures = make(map[uint32]paneRenderSnapshot)
		st.snapshot = &next
		r.publishSnapshot(&next)
	})

	ansi := r.CapturePaneText(1, true)
	if !strings.Contains(ansi, "\033]8;") {
		t.Fatalf("actor fallback should use current hyperlink capability, got %q", ansi)
	}
}
