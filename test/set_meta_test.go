package test

import (
	"strings"
	"testing"
)

func TestMetaSetSetsTaskAndBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "task=build", "branch=feat/foo")
	if out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "feat/foo") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "build") {
		t.Fatalf("list should show task, got:\n%s", list)
	}
}

func TestMetaSetSetsPRAppendedToBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "branch=main", "pr=42")

	list := h.runCmd("list")
	if !strings.Contains(list, "main") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "#42") {
		t.Fatalf("list should show PR number, got:\n%s", list)
	}
}

func TestMetaSetClearsBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("meta", "set", "pane-1", "branch=feat/bar")
	list := h.runCmd("list")
	if !strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be set, got:\n%s", list)
	}

	// Clear branch by setting to empty
	h.runCmd("meta", "set", "pane-1", "branch=")
	list = h.runCmd("list")
	if strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be cleared, got:\n%s", list)
	}
}

func TestMetaSetAllowsGenericKeys(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "bogus=value")
	if out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "bogus=value") {
		t.Fatalf("list should show generic metadata, got:\n%s", list)
	}
}

func TestMetaSetRejectsInvalidFormat(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "pane-1", "noequalssign")
	if !strings.Contains(out, "invalid key=value") {
		t.Fatalf("expected format error, got: %q", out)
	}
}

func TestMetaSetRejectsMissingArgs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set")
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage error, got: %q", out)
	}
}

func TestMetaSetRejectsUnknownPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("meta", "set", "no-such-pane", "task=x")
	if out == "" {
		t.Fatal("expected error for unknown pane")
	}
}

func TestListShowsBranchHeader(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	list := h.runCmd("list")
	if !strings.Contains(list, "BRANCH") {
		t.Fatalf("list header should contain BRANCH column, got:\n%s", list)
	}
}
