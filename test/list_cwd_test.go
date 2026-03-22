package test

import (
	"strings"
	"testing"
)

func TestListShowsPaneCwd(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	list := h.runCmd("list")
	if !strings.Contains(list, "CWD") {
		t.Fatalf("list should show CWD header, got:\n%s", list)
	}
}

func TestListNoCwdFlag(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	list := h.runCmd("list", "--no-cwd")
	if strings.Contains(list, "CWD") {
		t.Fatalf("list --no-cwd should omit CWD header:\n%s", list)
	}
	if !strings.Contains(list, "pane-1") {
		t.Fatalf("list --no-cwd should still list panes, got:\n%s", list)
	}
}
