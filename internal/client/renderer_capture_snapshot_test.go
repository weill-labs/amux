package client

import (
	"strings"
	"testing"
	"time"
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
	case <-time.After(50 * time.Millisecond):
		close(actorRelease)
		out := <-captureDone
		t.Fatalf("capture waited for renderer actor; output after release was %q", out)
	}

	close(actorRelease)
}
