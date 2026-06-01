package server

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

func TestQueuedCommandDebugScrollbackReportsPaneAndSessionTotals(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := sess.ownPane(mux.NewProxyPaneWithScrollback(1, mux.PaneMeta{
		Name:  "pane-1",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 20, 2, 3, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
		return len(data), nil
	}))
	p1.SetRetainedHistory([]string{"base-1", "base-2", "base-3"})
	for line := 1; line <= 4; line++ {
		p1.FeedOutput([]byte(fmt.Sprintf("live-%d\r\n", line)))
	}
	p2 := newTestPane(sess, 2, "pane-2")
	window := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, p1, p2)

	res := runTestCommand(t, srv, sess, "debug-scrollback")
	if res.cmdErr != "" {
		t.Fatalf("debug-scrollback error: %s", res.cmdErr)
	}

	for _, want := range []string{
		"Scrollback memory estimate for session test-command",
		"PANE",
		"LIMIT",
		"BASE",
		"LIVE",
		"EFFECTIVE",
		"ESTIMATED",
		"pane-1",
		"pane-2",
		"Totals:",
		"panes=2",
		"base=3",
		"live=3",
		"resident=6",
		"effective=3",
	} {
		if !strings.Contains(res.output, want) {
			t.Fatalf("debug-scrollback output missing %q:\n%s", want, res.output)
		}
	}
}

func TestQueuedCommandDebugScrollbackRejectsArgs(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	res := runTestCommand(t, srv, sess, "debug-scrollback", "--json")
	if res.cmdErr != "debug-scrollback does not accept arguments" {
		t.Fatalf("cmdErr = %q, want usage error", res.cmdErr)
	}
}
