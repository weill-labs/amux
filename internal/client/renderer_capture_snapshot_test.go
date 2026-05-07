package client

import (
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

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
