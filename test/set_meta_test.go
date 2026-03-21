package test

import (
	"strings"
	"testing"
)

func TestSetMetaSetsTaskAndBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-meta", "pane-1", "task=build", "branch=feat/foo")
	if out != "" {
		t.Fatalf("set-meta returned unexpected output: %q", out)
	}

	list := h.runCmd("list")
	if !strings.Contains(list, "feat/foo") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "build") {
		t.Fatalf("list should show task, got:\n%s", list)
	}
}

func TestSetMetaSetsPRAppendedToBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-meta", "pane-1", "branch=main", "pr=42")

	list := h.runCmd("list")
	if !strings.Contains(list, "main") {
		t.Fatalf("list should show branch, got:\n%s", list)
	}
	if !strings.Contains(list, "#42") {
		t.Fatalf("list should show PR number, got:\n%s", list)
	}
}

func TestSetMetaClearsBranch(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-meta", "pane-1", "branch=feat/bar")
	list := h.runCmd("list")
	if !strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be set, got:\n%s", list)
	}

	// Clear branch by setting to empty
	h.runCmd("set-meta", "pane-1", "branch=")
	list = h.runCmd("list")
	if strings.Contains(list, "feat/bar") {
		t.Fatalf("branch should be cleared, got:\n%s", list)
	}
}

func TestSetMetaRejectsUnknownKey(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-meta", "pane-1", "bogus=value")
	if !strings.Contains(out, "unknown meta key") {
		t.Fatalf("expected unknown key error, got: %q", out)
	}
}

func TestSetMetaRejectsInvalidFormat(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-meta", "pane-1", "noequalssign")
	if !strings.Contains(out, "invalid key=value") {
		t.Fatalf("expected format error, got: %q", out)
	}
}

func TestSetMetaRejectsMissingArgs(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-meta")
	if !strings.Contains(out, "usage") {
		t.Fatalf("expected usage error, got: %q", out)
	}
}

func TestSetMetaRejectsUnknownPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-meta", "no-such-pane", "task=x")
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
