package test

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

// attachClient attaches a new headless client to the harness's session.
func (h *ServerHarness) attachClient(cols, rows int) *headlessClient {
	h.tb.Helper()
	sockPath := server.SocketPath(h.session)
	hc, err := newHeadlessClient(sockPath, h.session, cols, rows)
	if err != nil {
		h.tb.Fatalf("attaching %dx%d client: %v", cols, rows, err)
	}
	return hc
}

// assertLayoutSize verifies that the server's layout dimensions match the
// expected width and height by doing a fresh attach at the given terminal size.
func (h *ServerHarness) assertLayoutSize(cols, rows, wantW, wantH int) {
	h.tb.Helper()
	msg := h.attachAt(cols, rows)
	snap := msg.Layout
	if snap.Width != wantW {
		h.tb.Errorf("layout width: got %d, want %d", snap.Width, wantW)
	}
	if snap.Height != wantH {
		h.tb.Errorf("layout height: got %d, want %d", snap.Height, wantH)
	}
}

func TestMultiClientLatestAttachWins(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, smaller client (60×20).
	small := h.attachClient(60, 20)

	// Wait for the layout broadcast triggered by the second client's attach.
	gen := h.generation()
	h.waitLayout(gen)

	// Layout should follow the newly attached client.
	h.assertLayoutSize(60, 20, 60, 19)
	assertCaptureConsistent(t, h.captureJSON())

	// Disconnect the small client — removeClient broadcasts layout.
	gen = h.generation()
	small.close()
	h.waitLayout(gen)

	h.assertLayoutSize(80, 24, 80, 23)
	assertCaptureConsistent(t, h.captureJSON())
}

func TestMultiClientExpandOnLarger(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, larger client (120×40).
	large := h.attachClient(120, 40)

	// Wait for the layout broadcast triggered by the larger client.
	gen := h.generation()
	h.waitLayout(gen)

	// Layout should expand to 120×39 (the larger client's dimensions).
	h.assertLayoutSize(120, 40, 120, 39)
	assertCaptureConsistent(t, h.captureJSON())

	// Disconnect the large client — layout should shrink back to 80×23.
	gen = h.generation()
	large.close()
	h.waitLayout(gen)

	h.assertLayoutSize(80, 24, 80, 23)
	assertCaptureConsistent(t, h.captureJSON())
}

func TestMultiClientSmallClientSeesAllPanes(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Split so there are two panes side by side.
	h.splitV()

	// Attach a second, smaller client (40×12).
	small := h.attachClient(40, 12)
	defer small.close()

	// The small client's renderer rescales the layout proportionally.
	// Both panes should be visible in the capture.
	text := small.capture()
	if !strings.Contains(text, "pane-1") {
		t.Errorf("small client should see pane-1\ncapture:\n%s", text)
	}
	if !strings.Contains(text, "pane-2") {
		t.Errorf("small client should see pane-2\ncapture:\n%s", text)
	}
	assertCaptureConsistent(t, h.captureJSON())
}

func TestMultiClientResizeRecalculates(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, larger client (120×40).
	large := h.attachClient(120, 40)

	gen := h.generation()
	h.waitLayout(gen)

	// Send a resize from the primary (80×24) client, shrinking it to 70×20.
	// The client with the latest activity should now own the session size.
	gen = h.generation()
	h.client.resize(70, 20)
	h.waitLayout(gen)

	h.assertLayoutSize(70, 20, 70, 19)
	assertCaptureConsistent(t, h.captureJSON())

	large.close()
}

func TestMultiClientLatestClientShrinkRecalculates(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80x24

	large := h.attachClient(120, 40)
	defer large.close()

	gen := h.generation()
	large.resize(70, 20)
	h.waitLayout(gen)

	h.assertLayoutSize(70, 20, 70, 19)
	assertCaptureConsistent(t, h.captureJSON())
}

func TestMultiClientFocusTransfersSizeOwnership(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80x24

	large := h.attachClient(120, 40)
	defer large.close()

	gen := h.generation()
	h.waitLayout(gen)
	h.assertLayoutSize(120, 40, 120, 39)

	gen = h.generation()
	h.client.sendUIEvent(proto.UIEventClientFocusGained)
	h.waitLayout(gen)
	h.assertLayoutSize(80, 24, 80, 23)
	assertCaptureConsistent(t, h.captureJSON())

	gen = h.generation()
	large.sendUIEvent(proto.UIEventClientFocusGained)
	h.waitLayout(gen)
	h.assertLayoutSize(120, 40, 120, 39)
	assertCaptureConsistent(t, h.captureJSON())
}
