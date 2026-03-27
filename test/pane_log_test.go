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

func TestRefreshMetaUpdatesPaneCwdAndBranch(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)

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

	listOut := h.runCmd("list")
	if strings.Contains(listOut, wantListCwd) || strings.Contains(listOut, "meta-branch") {
		t.Fatalf("list reported pane metadata before refresh-meta:\n%s", listOut)
	}

	if out := strings.TrimSpace(h.runCmd("refresh-meta", "pane-1")); out != "" {
		t.Fatalf("refresh-meta returned unexpected output: %q", out)
	}

	listOut = h.runCmd("list")
	if !strings.Contains(listOut, wantListCwd) {
		t.Fatalf("list missing refreshed cwd %q:\n%s", wantListCwd, listOut)
	}
	if !strings.Contains(listOut, "meta-branch") {
		t.Fatalf("list missing refreshed git branch:\n%s", listOut)
	}
}

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
	h.sendKeys("pane-2", fmt.Sprintf("cd %q && echo CWD_READY", tempDir), "Enter")
	h.waitFor("pane-2", "CWD_READY")
	if out := strings.TrimSpace(h.runCmd("refresh-meta", "pane-2")); out != "" {
		t.Fatalf("refresh-meta returned unexpected output: %q", out)
	}
	wantListCwd := listingcmd.FormatListCwd(wantCwd, h.home, listingcmd.ListCwdWidth)
	listOut := h.runCmd("list")
	if !strings.Contains(listOut, wantListCwd) {
		logPath := filepath.Join(serverpkg.SocketDir(), h.session+".log")
		logData, _ := os.ReadFile(logPath)
		t.Fatalf("list did not report cached cwd %q after refresh-meta\n%s\nserver log:\n%s", wantListCwd, listOut, string(logData))
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
