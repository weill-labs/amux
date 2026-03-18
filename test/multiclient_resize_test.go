package test

import (
	"testing"

	"github.com/weill-labs/amux/internal/server"
)

func TestMultiClientLargestWins(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, smaller client (60×20).
	sockPath := server.SocketPath(h.session)
	small, err := newHeadlessClient(sockPath, h.session, 60, 20)
	if err != nil {
		t.Fatalf("attaching small client: %v", err)
	}

	// Wait for the layout broadcast triggered by the second client's attach.
	gen := h.generation()
	h.waitLayout(gen)

	// Layout should stay at 80×23 (the larger client's dimensions).
	msg := h.attachAt(80, 24)
	snap := msg.Layout
	if snap.Width != 80 {
		t.Errorf("width: got %d, want 80 (largest client)", snap.Width)
	}
	if snap.Height != 23 {
		t.Errorf("height: got %d, want 23 (largest client)", snap.Height)
	}

	// Disconnect the small client — removeClient broadcasts layout.
	gen = h.generation()
	small.close()
	h.waitLayout(gen)

	msg = h.attachAt(80, 24)
	snap = msg.Layout
	if snap.Width != 80 {
		t.Errorf("after disconnect width: got %d, want 80", snap.Width)
	}
	if snap.Height != 23 {
		t.Errorf("after disconnect height: got %d, want 23", snap.Height)
	}
}

func TestMultiClientExpandOnLarger(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, larger client (120×40).
	sockPath := server.SocketPath(h.session)
	large, err := newHeadlessClient(sockPath, h.session, 120, 40)
	if err != nil {
		t.Fatalf("attaching large client: %v", err)
	}

	// Wait for the layout broadcast triggered by the larger client.
	gen := h.generation()
	h.waitLayout(gen)

	// Layout should expand to 120×39 (the larger client's dimensions).
	msg := h.attachAt(120, 40)
	snap := msg.Layout
	if snap.Width != 120 {
		t.Errorf("width: got %d, want 120 (largest client)", snap.Width)
	}
	if snap.Height != 39 {
		t.Errorf("height: got %d, want 39 (largest client)", snap.Height)
	}

	// Disconnect the large client — layout should shrink back to 80×23.
	gen = h.generation()
	large.close()
	h.waitLayout(gen)

	msg = h.attachAt(80, 24)
	snap = msg.Layout
	if snap.Width != 80 {
		t.Errorf("after disconnect width: got %d, want 80", snap.Width)
	}
	if snap.Height != 23 {
		t.Errorf("after disconnect height: got %d, want 23", snap.Height)
	}
}

func TestMultiClientResizeRecalculates(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t) // 80×24

	// Attach a second, larger client (120×40).
	sockPath := server.SocketPath(h.session)
	large, err := newHeadlessClient(sockPath, h.session, 120, 40)
	if err != nil {
		t.Fatalf("attaching large client: %v", err)
	}

	gen := h.generation()
	h.waitLayout(gen)

	// Send a resize from the primary (80×24) client, shrinking it to 70×20.
	// The large client (120×40) is still the max, so layout stays at 120×39.
	gen = h.generation()
	h.client.resize(70, 20)
	h.waitLayout(gen)

	msg := h.attachAt(120, 40)
	snap := msg.Layout
	if snap.Width != 120 {
		t.Errorf("width: got %d, want 120 (large client still connected)", snap.Width)
	}
	if snap.Height != 39 {
		t.Errorf("height: got %d, want 39 (large client still connected)", snap.Height)
	}

	large.close()
}
