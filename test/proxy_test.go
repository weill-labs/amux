package test

import (
	"strings"
	"testing"
)

func TestInjectProxyAndUnsplice(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Inject a mock proxy pane for a fake remote host
	out := h.runCmd("_inject-proxy", "fake-host")
	if !strings.Contains(out, "Injected proxy pane") {
		t.Fatalf("unexpected inject output: %s", out)
	}

	// Verify the proxy pane appears in list
	list := h.runCmd("list")
	if !strings.Contains(list, "fake-host") {
		t.Fatalf("list should show fake-host pane, got:\n%s", list)
	}

	// Unsplice removes the proxy pane and replaces with a local pane
	out = h.runCmd("unsplice", "fake-host")
	if !strings.Contains(out, "Unspliced fake-host") {
		t.Fatalf("unexpected unsplice output: %s", out)
	}

	// Verify the proxy pane is gone
	list = h.runCmd("list")
	if strings.Contains(list, "fake-host") {
		t.Fatalf("list should not contain fake-host after unsplice, got:\n%s", list)
	}
}

func TestUnspliceNoProxy(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("unsplice", "nonexistent")
	if !strings.Contains(out, "no spliced panes") {
		t.Fatalf("expected error about no spliced panes, got: %s", out)
	}
}
