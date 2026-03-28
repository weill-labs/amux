package server

import (
	"strings"
	"testing"
)

func TestCommandEqualize(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

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
