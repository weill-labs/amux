package client

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func multiWindow80x23ActiveWindow(windowID uint32) *proto.LayoutSnapshot {
	snap := multiWindow80x23()
	if len(snap.Windows) == 0 {
		return snap
	}
	for _, ws := range snap.Windows {
		if ws.ID != windowID {
			continue
		}
		snap.ActiveWindowID = ws.ID
		snap.ActivePaneID = ws.ActivePaneID
		snap.Root = ws.Root
		return snap
	}
	return snap
}

func paneScreenContains(t *testing.T, r *Renderer, paneID uint32, substr string) bool {
	t.Helper()

	var found bool
	r.withActor(func(st *rendererActorState) {
		emu := st.emulators[paneID]
		if emu == nil {
			return
		}
		found = emu.ScreenContains(substr)
	})
	return found
}

func paneHasEmulator(t *testing.T, r *Renderer, paneID uint32) bool {
	t.Helper()

	var ok bool
	r.withActor(func(st *rendererActorState) {
		_, ok = st.emulators[paneID]
	})
	return ok
}

func TestClientRendererHiddenWindowPaneStaysColdAfterLayout(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())

	if !paneHasEmulator(t, cr.renderer, 1) {
		t.Fatal("visible pane should allocate an emulator during layout")
	}
	if !paneHasEmulator(t, cr.renderer, 2) {
		t.Fatal("second visible pane should allocate an emulator during layout")
	}
	if paneHasEmulator(t, cr.renderer, 3) {
		t.Fatal("hidden window pane should stay cold until it becomes visible or is captured")
	}
}

func TestClientRendererHiddenPaneOutputStaysColdUntilCapture(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())

	const hiddenLine = "hidden window output"
	cr.HandlePaneOutput(3, []byte(hiddenLine))

	if paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("hidden pane output should stay cold until capture or visibility change")
	}

	if got := cr.CapturePaneText(3, false); !strings.Contains(got, hiddenLine) {
		t.Fatalf("CapturePaneText(3) = %q, want substring %q", got, hiddenLine)
	}

	if !paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("capture should flush hidden pane output into the emulator")
	}
}

func TestClientRendererHiddenPaneOutputFlushesWhenPaneBecomesVisible(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())

	const hiddenLine = "logs pane output"
	cr.HandlePaneOutput(3, []byte(hiddenLine))

	if paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("hidden pane output should stay cold before the window becomes visible")
	}

	cr.HandleLayout(multiWindow80x23ActiveWindow(2))

	if !paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("window activation should flush the pane's buffered output before rendering")
	}

	if got := cr.CapturePaneText(3, false); !strings.Contains(got, hiddenLine) {
		t.Fatalf("CapturePaneText(3) after window switch = %q, want substring %q", got, hiddenLine)
	}
}

func TestClientRendererHiddenPaneOutputFlushesOnCopyModeEntry(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())

	const hiddenLine = "copy mode hidden output"
	cr.HandlePaneOutput(3, []byte(hiddenLine))

	if paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("hidden pane output should stay cold until copy mode reads the buffer")
	}

	cr.EnterCopyMode(3)
	cm := cr.CopyModeForPane(3)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-3")
	}
	if !strings.Contains(cm.LineText(0), hiddenLine) {
		t.Fatalf("copy mode line 0 = %q, want substring %q", cm.LineText(0), hiddenLine)
	}
	if !paneScreenContains(t, cr.renderer, 3, hiddenLine) {
		t.Fatal("copy mode entry should flush hidden pane output into the emulator")
	}
}

func TestHandleRenderMsgHiddenPaneOutputDoesNotScheduleRender(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	cr.HandleLayout(multiWindow80x23())
	cr.ShowPrefixMessage("No binding for C-a f")
	cr.Render()

	effects := cr.handleRenderMsg(&RenderMsg{
		Typ:    RenderMsgPaneOutput,
		PaneID: 3,
		Data:   []byte("hidden"),
	})

	if len(effects) != 0 {
		t.Fatalf("handleRenderMsg hidden pane effects = %v, want none", effects)
	}
	if cr.IsDirty() {
		t.Fatal("hidden pane output should not dirty the visible frame")
	}
	if got := cr.prefixMessage(); got != "No binding for C-a f" {
		t.Fatalf("hidden pane output should preserve the prefix message, got %q", got)
	}
	if got := cr.CapturePaneText(3, false); !strings.Contains(got, "hidden") {
		t.Fatalf("CapturePaneText(3) = %q, want substring %q", got, "hidden")
	}
}
