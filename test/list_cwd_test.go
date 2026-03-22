package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListShowsPaneCwd(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	target := filepath.Join(h.home, "src", "clients", "alpha", "beta", "gamma", "delta", "amux54")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", target, err)
	}

	h.sendKeys("pane-1", "cd "+target+" && echo CWD_READY", "Enter")
	h.waitFor("pane-1", "CWD_READY")
	h.waitIdle("pane-1")

	list := h.runCmd("list")
	for _, want := range []string{"CWD", "~/", "amux54"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list output missing %q:\n%s", want, list)
		}
	}
	if !strings.Contains(list, "~/…/") {
		t.Fatalf("list should show truncated home path with ellipsis:\n%s", list)
	}
}

func TestListNoCwdFlag(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	target := filepath.Join(h.home, "src", "clients", "alpha", "beta", "gamma", "delta", "amux54")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", target, err)
	}

	h.sendKeys("pane-1", "cd "+target+" && echo CWD_READY", "Enter")
	h.waitFor("pane-1", "CWD_READY")
	h.waitIdle("pane-1")

	list := h.runCmd("list", "--no-cwd")
	if strings.Contains(list, "CWD") {
		t.Fatalf("list --no-cwd should omit CWD header:\n%s", list)
	}
	if strings.Contains(list, "~/") || strings.Contains(list, "amux54") {
		t.Fatalf("list --no-cwd should omit cwd values:\n%s", list)
	}
}
