package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverpkg "github.com/weill-labs/amux/internal/server"
	listingcmd "github.com/weill-labs/amux/internal/server/commands/listing"
)

func TestPaneLogCLI(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	// The harness starts with one pane; pane-log should show its creation.
	out := h.runCmd("pane-log")
	for _, want := range []string{
		"TS",
		"EVENT",
		"ID",
		"PANE",
		"HOST",
		"CWD",
		"GIT_BRANCH",
		"REASON",
		"create",
		"pane-1",
		"local",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("pane-log missing %q:\n%s", want, out)
		}
	}
}

func TestPaneLogShowsExitReason(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	gen := h.generation()
	h.splitH()
	h.waitLayout(gen)

	// Wait for the shell in pane-2 to start before sending exit.
	h.waitForPaneContent("pane-2", "$", 5*time.Second)

	gen = h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen)

	out := h.runCmd("pane-log")
	if !strings.Contains(out, "exit") {
		t.Fatalf("pane-log should contain exit event:\n%s", out)
	}
	if !strings.Contains(out, "pane-2") {
		t.Fatalf("pane-log should mention pane-2:\n%s", out)
	}
}

func TestPaneLogSnapshotsExitContext(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	gen := h.generation()
	h.splitH()
	h.waitLayout(gen)
	h.waitForPaneContent("pane-2", "$", 5*time.Second)
	h.waitIdle("pane-2")

	tempDir := filepath.Join(h.home, "cwd-target")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", tempDir, err)
	}
	wantCwd := tempDir
	if resolved, err := filepath.EvalSymlinks(tempDir); err == nil && resolved != "" {
		wantCwd = resolved
	}
	h.sendKeys("pane-2", fmt.Sprintf("cd %q", tempDir), "Enter")
	h.waitIdle("pane-2")
	wantListCwd := listingcmd.FormatListCwd(wantCwd, h.home, listingcmd.ListCwdWidth)
	deadline := time.Now().Add(10 * time.Second)
	listOut := ""
	for time.Now().Before(deadline) {
		listOut = h.runCmd("list")
		if strings.Contains(listOut, "server not running") {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if strings.Contains(listOut, wantListCwd) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(listOut, wantListCwd) {
		logPath := filepath.Join(serverpkg.SocketDir(), h.session+".log")
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("list did not report cached cwd %q within 10s\n%s\nserver log:\n%s", wantListCwd, listOut, string(logData))
	}

	if out := h.runCmd("set-meta", "pane-2", "branch=feat/postmortem"); out != "" {
		t.Fatalf("set-meta returned unexpected output: %q", out)
	}

	gen = h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen)

	out := h.runCmd("pane-log")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		if fields[1] != "exit" || fields[3] != "pane-2" {
			continue
		}
		if fields[5] != wantCwd {
			t.Fatalf("exit row cwd = %q, want %q\n%s", fields[5], wantCwd, out)
		}
		if fields[6] != "feat/postmortem" {
			t.Fatalf("exit row git branch = %q, want %q\n%s", fields[6], "feat/postmortem", out)
		}
		return
	}

	t.Fatalf("pane-log missing exit row for pane-2 with exit context:\n%s", out)
}
