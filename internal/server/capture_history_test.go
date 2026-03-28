package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCaptureHistoryTrimsTrailingBlankRows(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newStandaloneProxyPane(1, "pane-1")
	pane.SetRetainedHistory([]string{"history-1", "history-2"})
	pane.FeedOutput([]byte("visible-1\r\nvisible-2"))

	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	historyRes := runTestCommand(t, srv, sess, "capture", "--history", "pane-1")
	if historyRes.cmdErr != "" {
		t.Fatalf("capture --history error = %q", historyRes.cmdErr)
	}
	if historyRes.output != "history-1\nhistory-2\nvisible-1\nvisible-2\n" {
		t.Fatalf("capture --history output = %q, want trimmed history + visible content", historyRes.output)
	}

	screenRes := runTestCommand(t, srv, sess, "capture", "pane-1")
	if screenRes.cmdErr != "" {
		t.Fatalf("capture error = %q", screenRes.cmdErr)
	}
	if screenRes.output != "visible-1\nvisible-2\n" {
		t.Fatalf("capture output = %q, want visible content without padding", screenRes.output)
	}
}
