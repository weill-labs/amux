package test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/server"
)

var (
	serverCheckpointVersionPattern = regexp.MustCompile(`const ServerCheckpointVersion = \d+`)
	crashCheckpointVersionPattern  = regexp.MustCompile(`const CrashVersion = \d+`)
)

func captureHistoryPaneFromAmux(tb testing.TB, h *AmuxHarness, pane string) proto.CapturePane {
	tb.Helper()

	out := h.runCmd("capture", "--history", "--format", "json", pane)
	var capture proto.CapturePane
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		tb.Fatalf("capture history JSON: %v\nraw:\n%s", err, out)
	}
	return capture
}

func waitForHistoryPaneCapture(tb testing.TB, h *AmuxHarness, pane string, timeout time.Duration, match func(proto.CapturePane) bool) proto.CapturePane {
	tb.Helper()

	var lastRaw string
	raw := waitForOutput(tb, timeout, func() string {
		lastRaw = h.runCmd("capture", "--history", "--format", "json", pane)
		return lastRaw
	}, func(out string) bool {
		var capture proto.CapturePane
		if err := json.Unmarshal([]byte(out), &capture); err != nil {
			return false
		}
		return match(capture)
	})

	var capture proto.CapturePane
	if err := json.Unmarshal([]byte(raw), &capture); err != nil {
		tb.Fatalf("capture history JSON: %v\nraw:\n%s\nlast raw:\n%s", err, raw, lastRaw)
	}
	return capture
}

func linesWithPrefix(lines []string, prefix string) []string {
	var out []string
	for _, line := range lines {
		if strings.Contains(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

func buildAmuxAtomic(binPath, buildCommit string) error {
	tmp, err := os.CreateTemp(filepath.Dir(binPath), ".amux-reload-*")
	if err != nil {
		return fmt.Errorf("creating temp binary path: %w", err)
	}
	tmpPath := tmp.Name()
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp binary path: %w", closeErr)
	}
	if err := os.Remove(tmpPath); err != nil {
		return fmt.Errorf("removing temp placeholder: %w", err)
	}
	if err := buildAmuxWithCommit(tmpPath, buildCommit); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming rebuilt binary into place: %w", err)
	}
	return nil
}

func rewriteBinaryAtomic(binPath string) error {
	src, err := os.Open(binPath)
	if err != nil {
		return fmt.Errorf("opening binary for rewrite: %w", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat binary for rewrite: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(binPath), ".amux-rewrite-*")
	if err != nil {
		return fmt.Errorf("creating temp rewrite path: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("chmod temp rewrite path: %w", err)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("copying binary for rewrite: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp rewrite path: %w", err)
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming rewritten binary into place: %w", err)
	}
	return nil
}

func buildAmuxAtomicWithCheckpointVersionBumps(binPath, buildCommit string) error {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}

	checkpointSrcPath := filepath.Join(repoRoot, "internal", "checkpoint", "checkpoint.go")
	checkpointSrc, err := os.ReadFile(checkpointSrcPath)
	if err != nil {
		return fmt.Errorf("reading server checkpoint source: %w", err)
	}
	serverVersionMatch := serverCheckpointVersionPattern.FindString(string(checkpointSrc))
	if serverVersionMatch == "" {
		return fmt.Errorf("finding server checkpoint version constant")
	}
	serverVersionOverride := serverCheckpointVersionPattern.ReplaceAllString(serverVersionMatch, fmt.Sprintf("const ServerCheckpointVersion = %d", currentServerCheckpointVersion(checkpointSrc)+1))
	checkpointOverride := serverCheckpointVersionPattern.ReplaceAllString(string(checkpointSrc), serverVersionOverride)

	crashSrcPath := filepath.Join(repoRoot, "internal", "checkpoint", "crash.go")
	crashSrc, err := os.ReadFile(crashSrcPath)
	if err != nil {
		return fmt.Errorf("reading crash checkpoint source: %w", err)
	}
	crashVersionMatch := crashCheckpointVersionPattern.FindString(string(crashSrc))
	if crashVersionMatch == "" {
		return fmt.Errorf("finding crash checkpoint version constant")
	}
	crashVersionOverride := crashCheckpointVersionPattern.ReplaceAllString(crashVersionMatch, fmt.Sprintf("const CrashVersion = %d", currentCrashCheckpointVersion(crashSrc)+1))
	crashOverride := crashCheckpointVersionPattern.ReplaceAllString(string(crashSrc), crashVersionOverride)

	tmpDir, err := os.MkdirTemp("", "amux-version-bump-*")
	if err != nil {
		return fmt.Errorf("creating overlay dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	checkpointOverridePath := filepath.Join(tmpDir, "checkpoint.go")
	if err := os.WriteFile(checkpointOverridePath, []byte(checkpointOverride), 0o600); err != nil {
		return fmt.Errorf("writing server checkpoint override: %w", err)
	}
	crashOverridePath := filepath.Join(tmpDir, "crash.go")
	if err := os.WriteFile(crashOverridePath, []byte(crashOverride), 0o600); err != nil {
		return fmt.Errorf("writing crash checkpoint override: %w", err)
	}

	overlayPath := filepath.Join(tmpDir, "overlay.json")
	overlayData, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{
		Replace: map[string]string{
			checkpointSrcPath: checkpointOverridePath,
			crashSrcPath:      crashOverridePath,
		},
	})
	if err != nil {
		return fmt.Errorf("encoding overlay: %w", err)
	}
	if err := os.WriteFile(overlayPath, overlayData, 0o600); err != nil {
		return fmt.Errorf("writing overlay: %w", err)
	}

	tmpBin, err := os.CreateTemp(filepath.Dir(binPath), ".amux-version-bump-*")
	if err != nil {
		return fmt.Errorf("creating temp binary path: %w", err)
	}
	tmpBinPath := tmpBin.Name()
	if closeErr := tmpBin.Close(); closeErr != nil {
		os.Remove(tmpBinPath)
		return fmt.Errorf("closing temp binary path: %w", closeErr)
	}
	if err := os.Remove(tmpBinPath); err != nil {
		return fmt.Errorf("removing temp placeholder: %w", err)
	}

	args := []string{"build", "-overlay", overlayPath}
	if os.Getenv("AMUX_TEST_RACE") == "1" {
		args = append(args, "-race")
	}
	if os.Getenv("GOCOVERDIR") != "" {
		args = append(args, "-cover", "-covermode=atomic")
	}
	if buildCommit != "" {
		args = append(args, "-ldflags", "-X main.BuildCommit="+buildCommit)
	}
	args = append(args, "-o", tmpBinPath, repoRoot)

	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		os.Remove(tmpBinPath)
		return fmt.Errorf("building version-bumped amux: %v\n%s", err, out)
	}
	if err := os.Rename(tmpBinPath, binPath); err != nil {
		os.Remove(tmpBinPath)
		return fmt.Errorf("renaming rebuilt binary into place: %w", err)
	}
	return nil
}

func currentServerCheckpointVersion(src []byte) int {
	match := serverCheckpointVersionPattern.FindStringSubmatch(string(src))
	if len(match) == 0 {
		return 0
	}
	var version int
	_, _ = fmt.Sscanf(match[0], "const ServerCheckpointVersion = %d", &version)
	return version
}

func currentCrashCheckpointVersion(src []byte) int {
	match := crashCheckpointVersionPattern.FindStringSubmatch(string(src))
	if len(match) == 0 {
		return 0
	}
	var version int
	_, _ = fmt.Sscanf(match[0], "const CrashVersion = %d", &version)
	return version
}

func runAmuxCommandWithBin(tb testing.TB, binPath, home, coverDir, session string, args ...string) string {
	tb.Helper()
	cmdArgs := append([]string{"-s", session}, args...)
	cmd := exec.Command(binPath, cmdArgs...)
	env := upsertEnv(os.Environ(), "HOME", home)
	if coverDir != "" {
		env = upsertEnv(env, "GOCOVERDIR", coverDir)
	}
	cmd.Env = env
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func waitForOutput(tb testing.TB, timeout time.Duration, fn func() string, match func(string) bool) string {
	tb.Helper()

	last := fn()
	if match(last) {
		return last
	}

	deadline := time.NewTimer(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		select {
		case <-deadline.C:
			return last
		case <-ticker.C:
			last = fn()
			if match(last) {
				return last
			}
		}
	}
}

func newPersistentReloadHarness(tb testing.TB, binPath string) *AmuxHarness {
	tb.Helper()
	return newAmuxHarnessWithBin(tb, binPath, "AMUX_EXIT_UNATTACHED=0")
}

func newPersistentReloadHarnessInDir(tb testing.TB, binPath, launchDir string) *AmuxHarness {
	tb.Helper()
	return newAmuxHarnessWithBinInDir(tb, binPath, launchDir, "AMUX_EXIT_UNATTACHED=0")
}

func TestHotReloadKeybinding(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo RELOADME", "Enter")
	if !h.waitFor("RELOADME", 3*time.Second) {
		t.Fatalf("RELOADME not visible\nScreen:\n%s", h.captureOuter())
	}

	reloadGen := h.generation()
	h.sendKeys("C-a", "r")
	h.waitForReloadedClient(reloadGen, 8*time.Second)

	// Send a marker command to confirm the shell is ready after reload.
	h.sendKeys("echo POSTRELOAD", "Enter")
	if !h.waitFor("POSTRELOAD", 8*time.Second) {
		t.Fatalf("shell not ready after reload\nScreen:\n%s", h.captureOuter())
	}

	screen := h.captureOuter()
	if strings.Contains(screen, "not found") {
		t.Errorf("Ctrl-a r should be consumed, not forwarded (no 'not found' error)\nScreen:\n%s", screen)
	}

	history := h.captureOuterHistory()
	if !strings.Contains(history, "RELOADME") {
		t.Errorf("RELOADME should be preserved in outer history after hot reload\nHistory:\n%s", history)
	}
}

func TestHotReloadAutoDetect(t *testing.T) {
	t.Parallel()

	privateBin := privateAmuxBin(t)
	h := newPersistentReloadHarness(t, privateBin)

	h.sendKeys("echo AUTORLD", "Enter")
	if !h.waitFor("AUTORLD", 3*time.Second) {
		t.Fatalf("AUTORLD not visible\nScreen:\n%s", h.captureOuter())
	}

	if err := rewriteBinaryAtomic(privateBin); err != nil {
		t.Fatalf("rewriting amux binary: %v", err)
	}

	if !h.waitFor("[pane-", 10*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	screen := h.captureOuter()
	if !strings.Contains(screen, "AUTORLD") || !strings.Contains(screen, "[pane-") {
		t.Errorf("AUTORLD should be visible after auto-reload\nScreen:\n%s", screen)
	}
}

func TestHotReloadRebuildConvergesFromOutsideRepoWithMismatchedInstallMetadata(t *testing.T) {
	t.Parallel()

	privateDir := t.TempDir()
	privateBin := filepath.Join(privateDir, "amux")
	if err := buildAmuxWithCommit(privateBin, "beforeoutside"); err != nil {
		t.Fatalf("building private amux binary: %v", err)
	}

	otherRepo := filepath.Join(t.TempDir(), "amux-other")
	if err := os.MkdirAll(filepath.Join(otherRepo, ".git"), 0755); err != nil {
		t.Fatalf("creating fake repo root: %v", err)
	}
	meta := []byte("source_repo=" + otherRepo + "\n")
	if err := os.WriteFile(privateBin+".install-meta", meta, 0644); err != nil {
		t.Fatalf("writing install metadata: %v", err)
	}

	plainDir := filepath.Join(t.TempDir(), "plain-dir")
	if err := os.MkdirAll(plainDir, 0755); err != nil {
		t.Fatalf("creating plain launch dir: %v", err)
	}

	h := newPersistentReloadHarnessInDir(t, privateBin, plainDir)

	before := waitForOutput(t, 10*time.Second, func() string {
		return h.runCmd("status")
	}, func(out string) bool {
		return strings.Contains(out, "build: beforeoutside")
	})
	if !strings.Contains(before, "build: beforeoutside") {
		t.Fatalf("status before binary rewrite = %q, want before build marker", before)
	}
	if err := buildAmuxAtomic(privateBin, "srvonlyreload"); err != nil {
		t.Fatalf("rewriting private amux binary atomically: %v", err)
	}

	after := waitForOutput(t, 10*time.Second, func() string {
		return h.runCmd("status")
	}, func(out string) bool {
		return strings.Contains(out, "build: srvonlyreload")
	})
	if !strings.Contains(after, "build: srvonlyreload") {
		t.Fatalf("status after binary rewrite = %q, want new build marker", after)
	}

	h.sendKeys("echo ONE_RELOAD_ONLY", "Enter")
	if !h.waitFor("ONE_RELOAD_ONLY", 5*time.Second) {
		t.Fatalf("inner client did not recover after binary rewrite\nScreen:\n%s", h.captureOuter())
	}
}

func TestServerHotReload(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.sendKeys("echo BEFORERLD", "Enter")
	h.waitFor("BEFORERLD", 3*time.Second)

	h.splitV()
	reloadGen := h.generation()

	h.runCmd("reload-server")
	h.waitForReloadedClient(reloadGen, 5*time.Second)

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	h.sendKeys("echo AFTERRLD", "Enter")
	if !h.waitFor("AFTERRLD", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("PTY should work after reload\nScreen:\n%s", screen)
	}

	listOut := h.runCmd("list")
	if !strings.Contains(listOut, "pane-1") || !strings.Contains(listOut, "pane-2") {
		t.Errorf("list should show both panes after reload, got:\n%s", listOut)
	}
}

func TestReloadServerExecsReplacementBinaryAfterAtomicInstall(t *testing.T) {
	t.Parallel()

	privateBin := filepath.Join(t.TempDir(), "amux")
	if err := buildAmuxWithCommit(privateBin, "oldbuild"); err != nil {
		t.Fatalf("building old amux binary: %v", err)
	}

	h := newPersistentReloadHarness(t, privateBin)

	before := h.runCmd("status")
	if !strings.Contains(before, "build: oldbuild") {
		t.Fatalf("status before reload = %q, want old build marker", before)
	}

	if err := buildAmuxAtomic(privateBin, "newbuild"); err != nil {
		t.Fatalf("atomically rebuilding amux binary: %v", err)
	}

	h.runCmd("reload-server")

	after := waitForOutput(t, 10*time.Second, func() string {
		return h.runCmd("status")
	}, func(out string) bool {
		return strings.Contains(out, "build: newbuild")
	})
	if !strings.Contains(after, "build: newbuild") {
		t.Fatalf("status after reload = %q, want new build marker", after)
	}
	if strings.Contains(after, "build: oldbuild") {
		t.Fatalf("status after reload should not report old build, got %q", after)
	}
}

func TestReloadServerUsesRequestingBinaryNotOriginalLaunchBinary(t *testing.T) {
	t.Parallel()

	oldBin := filepath.Join(t.TempDir(), "old-amux")
	if err := buildAmuxWithCommit(oldBin, "oldbuild"); err != nil {
		t.Fatalf("building old amux binary: %v", err)
	}

	newBin := filepath.Join(t.TempDir(), "new-amux")
	if err := buildAmuxWithCommit(newBin, "newbuild"); err != nil {
		t.Fatalf("building new amux binary: %v", err)
	}

	h := newPersistentReloadHarness(t, oldBin)

	before := runAmuxCommandWithBin(t, newBin, h.outer.home, h.outer.coverDir, h.inner, "status")
	if !strings.Contains(before, "build: oldbuild") {
		t.Fatalf("status before reload = %q, want old build marker", before)
	}

	runAmuxCommandWithBin(t, newBin, h.outer.home, h.outer.coverDir, h.inner, "reload-server")

	after := waitForOutput(t, 10*time.Second, func() string {
		return runAmuxCommandWithBin(t, newBin, h.outer.home, h.outer.coverDir, h.inner, "status")
	}, func(out string) bool {
		return strings.Contains(out, "build: newbuild")
	})
	if !strings.Contains(after, "build: newbuild") {
		t.Fatalf("status after reload = %q, want new build marker", after)
	}
	if strings.Contains(after, "build: oldbuild") {
		t.Fatalf("status after reload should not report old build, got %q", after)
	}
}

func TestServerAutoReload(t *testing.T) {
	t.Parallel()

	privateBin := privateAmuxBin(t)
	h := newPersistentReloadHarness(t, privateBin)

	h.sendKeys("echo SRVAUTO", "Enter")
	if !h.waitFor("SRVAUTO", 3*time.Second) {
		t.Fatalf("SRVAUTO not visible\nScreen:\n%s", h.captureOuter())
	}

	if err := rewriteBinaryAtomic(privateBin); err != nil {
		t.Fatalf("rewriting amux binary: %v", err)
	}

	if !h.waitFor("[pane-", 15*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after binary rebuild\nScreen:\n%s", screen)
	}

	if !h.waitFor("SRVAUTO", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("SRVAUTO should be visible after server auto-reload\nScreen:\n%s", screen)
	}
}

func TestServerHotReloadFallsBackToCrashCheckpointAcrossVersionBump(t *testing.T) {
	t.Parallel()

	privateBin := filepath.Join(t.TempDir(), "amux")
	if err := buildAmuxWithCommit(privateBin, "oldbuild"); err != nil {
		t.Fatalf("building old amux binary: %v", err)
	}

	h := newPersistentReloadHarness(t, privateBin)

	h.sendKeys("echo BEFOREBUMP", "Enter")
	if !h.waitFor("BEFOREBUMP", 3*time.Second) {
		t.Fatalf("BEFOREBUMP not visible\nScreen:\n%s", h.captureOuter())
	}

	h.splitV()
	uiBefore := h.uiGen()

	if err := buildAmuxAtomicWithCheckpointVersionBumps(privateBin, "newbuild"); err != nil {
		t.Fatalf("building version-bumped amux binary: %v", err)
	}

	h.runCmd("reload-server")
	h.waitUIGenChange(uiBefore, 15*time.Second)

	if !h.waitFor("[pane-", 15*time.Second) {
		t.Fatalf("session did not recover after version-bumped reload\nScreen:\n%s", h.captureOuter())
	}

	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 10*time.Second) {
		t.Fatalf("both panes should be visible after version-bumped reload\nScreen:\n%s", h.captureOuter())
	}

	after := waitForOutput(t, 10*time.Second, func() string {
		return h.runCmd("status")
	}, func(out string) bool {
		return strings.Contains(out, "build: newbuild")
	})
	if !strings.Contains(after, "build: newbuild") {
		t.Fatalf("status after reload = %q, want new build marker", after)
	}

	h.sendKeys("echo AFTERBUMP", "Enter")
	if !h.waitFor("AFTERBUMP", 5*time.Second) {
		t.Fatalf("PTY should work after version-bumped reload\nScreen:\n%s", h.captureOuter())
	}

	logPath := filepath.Join(server.SocketDir(), h.inner+".log")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading server log %s: %v", logPath, err)
	}
	wantLog := "reload checkpoint v" + strconv.Itoa(checkpoint.ServerCheckpointVersion) + " incompatible with new binary checkpoint v" + strconv.Itoa(checkpoint.ServerCheckpointVersion+1)
	if !strings.Contains(string(logData), wantLog) {
		t.Fatalf("server log missing pre-exec incompatibility warning %q\nserver log:\n%s", wantLog, logData)
	}
}

func TestServerReloadPreservesHistoryCapture(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reload-history-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 45); do echo \"RLDHIST-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("RLDHIST-45", 5*time.Second) {
		t.Fatalf("expected scrollback source before reload\nScreen:\n%s", h.captureOuter())
	}

	before := h.runCmd("capture", "--history", "pane-1")
	if !strings.Contains(before, "RLDHIST-01") {
		t.Fatalf("history capture before reload should include earliest retained line, got:\n%s", before)
	}

	h.runCmd("reload-server")
	if !h.waitFor("[pane-", 5*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	after := waitForHistoryPaneCapture(t, h, "pane-1", 10*time.Second, func(capture proto.CapturePane) bool {
		all := strings.Join(append(append([]string(nil), capture.History...), capture.Content...), "\n")
		return strings.Contains(all, "RLDHIST-01") && strings.Contains(all, "RLDHIST-45")
	})
	all := strings.Join(append(append([]string(nil), after.History...), after.Content...), "\n")
	if !strings.Contains(all, "RLDHIST-01") || !strings.Contains(all, "RLDHIST-45") {
		t.Fatalf("history capture should survive reload, got:\n%s", all)
	}
}

func TestServerReloadPreservesConfiguredHistoryLimit(t *testing.T) {
	t.Parallel()

	h := newAmuxHarnessWithConfig(t, `
scrollback_lines = 5
`)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reload-history-limit-%s.sh", h.session))
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nfor i in $(seq -w 1 45); do echo \"RLDCFG-$i\"; done\n"), 0755); err != nil {
		t.Fatalf("writing history script: %v", err)
	}
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("RLDCFG-45", 5*time.Second) {
		t.Fatalf("expected scrollback source before reload\nScreen:\n%s", h.captureOuter())
	}

	before := captureHistoryPaneFromAmux(t, h, "pane-1")
	if got := len(linesWithPrefix(before.History, "RLDCFG-")); got != 5 {
		t.Fatalf("history markers before reload = %d, want 5\nhistory=%v", got, before.History)
	}

	h.runCmd("reload-server")
	if !h.waitFor("[pane-", 5*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	after := waitForHistoryPaneCapture(t, h, "pane-1", 10*time.Second, func(capture proto.CapturePane) bool {
		return len(linesWithPrefix(capture.History, "RLDCFG-")) == 5
	})
	if got := len(linesWithPrefix(after.History, "RLDCFG-")); got != 5 {
		t.Fatalf("history markers after reload = %d, want 5\nhistory=%v", got, after.History)
	}

	all := append(append([]string(nil), after.History...), after.Content...)
	if strings.Contains(strings.Join(all, "\n"), "RLDCFG-01") {
		t.Fatalf("oldest marker should not survive configured history cap, got:\n%v", all)
	}
	if !strings.Contains(strings.Join(all, "\n"), "RLDCFG-45") {
		t.Fatalf("latest marker should survive reload, got:\n%v", all)
	}
}

func TestServerReloadPreservesGeneration(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	// Do a split to bump the generation counter
	h.splitH()

	genBefore := h.generation()
	if genBefore == 0 {
		t.Fatalf("generation should be > 0 after split, got 0")
	}

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	genAfter := h.generation()
	if genAfter < genBefore {
		t.Errorf("generation should survive reload: before=%d, after=%d", genBefore, genAfter)
	}
}

func TestServerReloadCaptureRetry(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.runCmd("reload-server")

	// Wait for the client to reattach after reload before issuing capture.
	// On slow CI runners the reattach can take longer than the server-side
	// captureResponseTimeout (3s), causing a spurious "client unresponsive".
	if !h.waitFor("[pane-", 15*time.Second) {
		t.Fatalf("session did not recover after reload\nScreen:\n%s", h.captureOuter())
	}

	// The retry loop should wait for the client to reconnect rather than
	// returning "no client attached".
	out := waitForOutput(t, 10*time.Second, func() string {
		return h.runCmd("capture", "--format", "json")
	}, func(out string) bool {
		return strings.Contains(out, "pane-1")
	})
	if strings.Contains(out, "no client attached") {
		t.Fatalf("capture should retry after reload, got: %s", out)
	}
	if !strings.Contains(out, "pane-1") {
		t.Errorf("capture JSON should contain pane-1 after reload, got: %s", out)
	}
}

func TestServerReloadBorderColors(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	h.splitV()

	gen := h.generation()
	h.sendKeys("C-a", "h")
	h.waitLayout(gen)

	ansiBefore := h.captureANSI()
	colorsBefore := extractBorderColors(pickContentLine(ansiBefore))

	h.runCmd("reload-server")

	if !h.waitFor("[pane-", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("session did not recover after reload\nScreen:\n%s", screen)
	}
	if !h.waitForFunc(func(s string) bool {
		return strings.Contains(s, "[pane-1]") && strings.Contains(s, "[pane-2]")
	}, 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("both panes should be visible after reload\nScreen:\n%s", screen)
	}

	ansiAfter := h.captureANSI()
	colorsAfter := extractBorderColors(pickContentLine(ansiAfter))

	if len(colorsBefore) == 0 {
		t.Fatalf("no border colors found before reload\nScreen:\n%s", ansiBefore)
	}
	if len(colorsAfter) == 0 {
		t.Fatalf("no border colors found after reload\nScreen:\n%s", ansiAfter)
	}

	if colorsBefore[0] != colorsAfter[0] {
		t.Errorf("border color changed after reload:\n  before: %s\n  after:  %s", colorsBefore[0], colorsAfter[0])
	}
}

func TestServerReloadTUIRedraw(t *testing.T) {
	t.Parallel()
	h := newAmuxHarness(t)

	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-tui-%s.sh", h.session))
	os.WriteFile(scriptPath, []byte(`#!/bin/bash
printf '\033[?1049h'
draw() { printf '\033[2J\033[H'; echo TUIMARK_OK; }
trap draw WINCH
draw
while true; do sleep 60; done
`), 0755)
	t.Cleanup(func() { os.Remove(scriptPath) })

	h.sendKeys(scriptPath, "Enter")
	if !h.waitFor("TUIMARK_OK", 5*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("TUI script did not start\nScreen:\n%s", screen)
	}

	h.runCmd("reload-server")

	if !h.waitFor("TUIMARK_OK", 15*time.Second) {
		screen := h.captureOuter()
		t.Fatalf("TUI marker should be visible after reload (SIGWINCH redraw)\nScreen:\n%s", screen)
	}
}
