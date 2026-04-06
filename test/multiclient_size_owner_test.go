package test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestBackgroundAttachedClientCommandDoesNotStealSizeOwner(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t) // primary interactive client at 80x24
	h.splitV()

	secondary := h.attachClient(120, 40)
	defer secondary.close()

	// Let the secondary attach win first, then hand size ownership back to the
	// primary client via an explicit focus event. The regression is that a later
	// command sent over the background client should not flip ownership again.
	gen := h.generation()
	h.waitLayout(gen)

	gen = h.generation()
	h.client.sendUIEvent(proto.UIEventClientFocusGained)
	h.waitLayout(gen)

	visible := mustHeadlessCaptureJSON(t, h.client)
	pane := jsonPaneFor(t, visible, "pane-1")
	if pane.Position == nil {
		t.Fatal("pane-1 position missing")
	}

	wantCols := pane.Position.Width
	wantRows := pane.Position.Height - 1
	wantMarker := fmt.Sprintf("SIZE-OWNER %dx%d", wantCols, wantRows)

	res := secondary.runCommand(
		"send-keys",
		"pane-1",
		"printf '%s %sx%s\\n' \"SIZE-OWNER\" \"$(tput cols)\" \"$(tput lines)\"",
		"Enter",
	)
	if res.CmdErr != "" {
		t.Fatalf("secondary send-keys error: %s", res.CmdErr)
	}

	if !waitForHeadlessCaptureJSON(h.client, 5*time.Second, func(capture proto.CaptureJSON) bool {
		p := jsonPaneFor(t, capture, "pane-1")
		return strings.Contains(strings.Join(p.Content, "\n"), wantMarker)
	}) {
		got := mustHeadlessCaptureJSON(t, h.client)
		p := jsonPaneFor(t, got, "pane-1")
		t.Fatalf(
			"primary client never observed %q after background client command\nprimary pane width=%d rows=%d\ncontent:\n%s",
			wantMarker,
			wantCols,
			wantRows,
			strings.Join(p.Content, "\n"),
		)
	}
}

func mustHeadlessCaptureJSON(t *testing.T, hc *headlessClient) proto.CaptureJSON {
	t.Helper()

	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(hc.renderer.CaptureJSON(nil)), &capture); err != nil {
		t.Fatalf("headless capture json: %v", err)
	}
	return capture
}

func waitForHeadlessCaptureJSON(hc *headlessClient, timeout time.Duration, fn func(proto.CaptureJSON) bool) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		var capture proto.CaptureJSON
		if err := json.Unmarshal([]byte(hc.renderer.CaptureJSON(nil)), &capture); err == nil && fn(capture) {
			return true
		}
		<-ticker.C
	}
	return false
}
