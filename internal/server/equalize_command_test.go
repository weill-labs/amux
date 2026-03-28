package server

import (
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func TestCommandEqualize(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	w1 := newTestWindowWithPanes(t, sess, 1, "main", p1, p2)
	setSessionLayoutForTest(t, sess, w1.ID, []*mux.Window{w1}, p1, p2)

	bad := runTestCommand(t, srv, sess, "equalize", "--bogus")
	if bad.cmdErr != `equalize: unknown mode "--bogus" (use --vertical or --all)` {
		t.Fatalf("equalize invalid mode error = %q", bad.cmdErr)
	}

	res := runTestCommand(t, srv, sess, "equalize", "--all")
	if res.cmdErr != "" {
		t.Fatalf("equalize error: %s", res.cmdErr)
	}
	if !strings.Contains(res.output, "Equalized") {
		t.Fatalf("equalize output = %q, want Equalized confirmation", res.output)
	}
}
