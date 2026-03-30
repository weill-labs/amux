package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	serverpkg "github.com/weill-labs/amux/internal/server"
	listingcmd "github.com/weill-labs/amux/internal/server/commands/listing"
)

func waitForListMetadata(t *testing.T, h *ServerHarness, want ...string) string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out := h.runCmd("list")
		ok := true
		for _, needle := range want {
			if !strings.Contains(out, needle) {
				ok = false
				break
			}
		}
		if ok {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}

	out := h.runCmd("list")
	t.Fatalf("timed out waiting for list metadata %v:\n%s", want, out)
	return ""
}

func TestIdleRefreshUpdatesPaneCwdAndBranch(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithOptions(t, 80, 24, "", false, false, "AMUX_DISABLE_META_REFRESH=0")

	repoDir := filepath.Join(h.home, "refresh-meta-repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", repoDir, err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "init", "-b", "meta-branch").CombinedOutput(); err != nil {
		t.Fatalf("git init repo: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.email", "amux-tests@example.com").CombinedOutput(); err != nil {
		t.Fatalf("git config user.email: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "config", "user.name", "amux tests").CombinedOutput(); err != nil {
		t.Fatalf("git config user.name: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("meta refresh\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", repoDir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	h.sendKeys("pane-1", fmt.Sprintf("cd %q && echo META_READY", repoDir), "Enter")
	h.waitFor("pane-1", "META_READY")
	h.waitIdle("pane-1")

	wantCwd := repoDir
	if resolved, err := filepath.EvalSymlinks(repoDir); err == nil && resolved != "" {
		wantCwd = resolved
	}
	wantListCwd := listingcmd.FormatListCwd(wantCwd, h.home, listingcmd.ListCwdWidth)

	listOut := waitForListMetadata(t, h, wantListCwd, "meta-branch")
	if !strings.Contains(listOut, wantListCwd) {
		t.Fatalf("list missing idle-refreshed cwd %q:\n%s", wantListCwd, listOut)
	}
	if !strings.Contains(listOut, "meta-branch") {
		t.Fatalf("list missing idle-refreshed git branch:\n%s", listOut)
	}
}

func TestLogPanesCLI(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

	out := h.runCmd("log", "panes")
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
			t.Fatalf("log panes missing %q:\n%s", want, out)
		}
	}
}

func TestLogPanesShowsExitReason(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	gen := h.generation()
	h.splitH()
	h.waitLayout(gen)

	h.waitForPaneContent("pane-2", "$", 5*time.Second)

	gen = h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen)

	out := h.runCmd("log", "panes")
	if !strings.Contains(out, "exit") {
		t.Fatalf("log panes should contain exit event:\n%s", out)
	}
	if !strings.Contains(out, "pane-2") {
		t.Fatalf("log panes should mention pane-2:\n%s", out)
	}
}

func TestLogPanesSnapshotsExitContext(t *testing.T) {
	t.Parallel()

	h := newServerHarnessWithOptions(t, 80, 24, "", false, false, "AMUX_DISABLE_META_REFRESH=0")
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
	h.sendKeys("pane-2", fmt.Sprintf("cd %q && echo CWD_READY", tempDir), "Enter")
	h.waitFor("pane-2", "CWD_READY")
	h.waitIdle("pane-2")

	wantListCwd := listingcmd.FormatListCwd(wantCwd, h.home, listingcmd.ListCwdWidth)
	listOut := waitForListMetadata(t, h, wantListCwd)
	if !strings.Contains(listOut, wantListCwd) {
		logPath := filepath.Join(serverpkg.SocketDir(), h.session+".log")
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("list did not report cached cwd %q after idle refresh\n%s\nserver log:\n%s", wantListCwd, listOut, string(logData))
	}

	if out := h.runCmd("meta", "set", "pane-2", "branch=feat/postmortem"); out != "" {
		t.Fatalf("meta set returned unexpected output: %q", out)
	}

	gen = h.generation()
	h.sendKeys("pane-2", "exit", "Enter")
	h.waitLayout(gen)

	out := h.runCmd("log", "panes")
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

	t.Fatalf("log panes missing exit row for pane-2 with exit context:\n%s", out)
}
