package client

import (
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type captureSnapshotFakeEmulator struct {
	width, height int
	scrollback    []string
	screen        []string
	pushed        uint64
	screenChanged bool
	lineReads     []int
	renderCalls   int
}

func newCaptureSnapshotFakeEmulator(lines []string, pushed uint64) *captureSnapshotFakeEmulator {
	return &captureSnapshotFakeEmulator{
		width:      4,
		height:     2,
		scrollback: append([]string(nil), lines...),
		screen:     []string{"screen-0", "screen-1"},
		pushed:     pushed,
	}
}

func (e *captureSnapshotFakeEmulator) Write(data []byte) (int, error) { return len(data), nil }
func (e *captureSnapshotFakeEmulator) DrainScreenChanges() bool {
	changed := e.screenChanged
	e.screenChanged = false
	return changed
}
func (e *captureSnapshotFakeEmulator) Read([]byte) (int, error)   { return 0, io.EOF }
func (e *captureSnapshotFakeEmulator) Close() error               { return nil }
func (e *captureSnapshotFakeEmulator) Render() string             { e.renderCalls++; return strings.Join(e.screen, "\n") }
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
	return e.Render()
}
func (e *captureSnapshotFakeEmulator) HasCursorBlock() bool { return false }
func (e *captureSnapshotFakeEmulator) CursorBlockPosition() (int, int, bool) {
	return 0, 0, false
}
func (e *captureSnapshotFakeEmulator) ScreenLineText(y int) string {
	if y < 0 || y >= len(e.screen) {
		return ""
	}
	return e.screen[y]
}
func (e *captureSnapshotFakeEmulator) LineWrapped(int) bool       { return false }
func (e *captureSnapshotFakeEmulator) ScreenContains(string) bool { return false }
func (e *captureSnapshotFakeEmulator) CellAt(int, int) *uv.Cell   { return nil }
func (e *captureSnapshotFakeEmulator) IsAltScreen() bool          { return false }
func (e *captureSnapshotFakeEmulator) MouseProtocol() mux.MouseProtocol {
	return mux.MouseProtocol{}
}
func (e *captureSnapshotFakeEmulator) EncodeMouse(mouse.Event, int, int) []byte {
	return nil
}

func scrollbackState(lines []string, pushed uint64) paneScrollbackSnapshotState {
	return paneScrollbackSnapshotState{
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
		prev       paneScrollbackSnapshotState
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
			snap, state := capturePaneRenderSnapshot(emu, tt.prev)

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

func TestCapturePaneRenderSnapshotReusesRenderedANSIWhenScreenUnchanged(t *testing.T) {
	t.Parallel()

	emu := newCaptureSnapshotFakeEmulator(nil, 0)
	emu.screen = []string{"cached", "screen"}
	emu.screenChanged = true

	first, state := capturePaneRenderSnapshot(emu, paneScrollbackSnapshotState{})
	if got, want := emu.renderCalls, 1; got != want {
		t.Fatalf("initial Render calls = %d, want %d", got, want)
	}

	emu.renderCalls = 0
	emu.screenChanged = false
	second, _ := capturePaneRenderSnapshot(emu, state)

	if got := emu.renderCalls; got != 0 {
		t.Fatalf("Render calls after unchanged screen = %d, want 0", got)
	}
	if second.rendered != first.rendered {
		t.Fatalf("rendered ANSI changed after unchanged screen: got %q, want %q", second.rendered, first.rendered)
	}
	if second.renderedNoCursor != first.renderedNoCursor {
		t.Fatalf("cursorless ANSI changed after unchanged screen: got %q, want %q", second.renderedNoCursor, first.renderedNoCursor)
	}
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
