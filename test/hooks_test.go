package test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/weill-labs/amux/internal/server"
)

func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer watcher.Close()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := watcher.Add(dir); err != nil {
		t.Fatalf("watch %s: %v", dir, err)
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return false
		case event := <-watcher.Events:
			if filepath.Clean(event.Name) != filepath.Clean(path) {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				return true
			}
		case err := <-watcher.Errors:
			t.Logf("fsnotify error for %s: %v", path, err)
		}
	}
}

func waitForFileContent(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer watcher.Close()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := watcher.Add(dir); err != nil {
		t.Fatalf("watch %s: %v", dir, err)
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return ""
		case event := <-watcher.Events:
			if filepath.Clean(event.Name) != filepath.Clean(path) {
				continue
			}
			data, err := os.ReadFile(path)
			if err == nil && len(data) > 0 {
				return string(data)
			}
		case err := <-watcher.Errors:
			t.Logf("fsnotify error for %s: %v", path, err)
		}
	}
}

func waitForHook(t *testing.T, h *ServerHarness, event, pane string, after uint64) {
	t.Helper()
	args := []string{"wait-hook", event, "--after", strconv.FormatUint(after, 10), "--timeout", "5s"}
	if pane != "" {
		args = append(args, "--pane", pane)
	}
	out := h.runCmd(args...)
	if strings.Contains(out, "timeout") {
		t.Fatalf("wait-hook %s timed out: %s", event, out)
	}
}

func TestSetHookAndListHooks(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-hook", "on-idle", "echo idle")
	if strings.Contains(out, "error") || strings.Contains(out, "unknown") {
		t.Fatalf("set-hook failed: %s", out)
	}

	out = h.runCmd("set-hook", "on-activity", "echo active")
	if strings.Contains(out, "error") || strings.Contains(out, "unknown") {
		t.Fatalf("set-hook failed: %s", out)
	}

	out = h.runCmd("list-hooks")
	if !strings.Contains(out, "on-idle") {
		t.Errorf("list-hooks should show on-idle, got:\n%s", out)
	}
	if !strings.Contains(out, "echo idle") {
		t.Errorf("list-hooks should show command, got:\n%s", out)
	}
	if !strings.Contains(out, "on-activity") {
		t.Errorf("list-hooks should show on-activity, got:\n%s", out)
	}
}

func TestUnsetHook(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-hook", "on-idle", "echo a")
	h.runCmd("set-hook", "on-idle", "echo b")

	out := h.runCmd("unset-hook", "on-idle", "0")
	if strings.Contains(out, "error") {
		t.Fatalf("unset-hook failed: %s", out)
	}

	out = h.runCmd("list-hooks")
	if strings.Contains(out, "echo a") {
		t.Errorf("echo a should have been removed, got:\n%s", out)
	}
	if !strings.Contains(out, "echo b") {
		t.Errorf("echo b should remain, got:\n%s", out)
	}
}

func TestUnsetHookAll(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.runCmd("set-hook", "on-idle", "echo a")
	h.runCmd("set-hook", "on-idle", "echo b")
	h.runCmd("set-hook", "on-activity", "echo c")

	h.runCmd("unset-hook", "on-idle")

	out := h.runCmd("list-hooks")
	if strings.Contains(out, "on-idle") {
		t.Errorf("all on-idle hooks should be removed, got:\n%s", out)
	}
	if !strings.Contains(out, "on-activity") {
		t.Errorf("on-activity should remain, got:\n%s", out)
	}
}

func TestSetHookInvalidEvent(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	out := h.runCmd("set-hook", "invalid-event", "echo test")
	if !strings.Contains(out, "unknown hook event") {
		t.Errorf("expected error for invalid event, got:\n%s", out)
	}
}

func TestHookOnIdleFires(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "idle-fired")
	after := strings.TrimSpace(h.runCmd("hook-gen"))
	afterGen, err := strconv.ParseUint(after, 10, 64)
	if err != nil {
		t.Fatalf("parse hook-gen: %v (output %q)", err, after)
	}

	h.waitIdle("pane-1")
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != "idle" {
		t.Fatalf("initial event: got %q, want idle", ev.Type)
	}

	h.runCmd("set-hook", "on-idle", "touch "+marker)

	h.sendKeys("pane-1", "echo TRIGGER_ACTIVITY", "Enter")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != "busy" {
		t.Fatalf("activity event: got %q, want busy", ev.Type)
	}
	if ev := mustReadEvent(t, scanner, 10*time.Second); ev.Type != "idle" {
		t.Fatalf("post-activity event: got %q, want idle", ev.Type)
	}
	waitForHook(t, h, "on-idle", "pane-1", afterGen)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("on-idle hook marker missing after wait-hook: %v", err)
	}
}

func TestHookOnActivityFires(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "activity-fired")
	after := strings.TrimSpace(h.runCmd("hook-gen"))
	afterGen, err := strconv.ParseUint(after, 10, 64)
	if err != nil {
		t.Fatalf("parse hook-gen: %v (output %q)", err, after)
	}

	h.waitIdle("pane-1")
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != "idle" {
		t.Fatalf("initial event: got %q, want idle", ev.Type)
	}

	h.runCmd("set-hook", "on-activity", "touch "+marker)
	h.sendKeys("pane-1", "echo TRIGGER", "Enter")
	if ev := mustReadEvent(t, scanner, 5*time.Second); ev.Type != "busy" {
		t.Fatalf("activity event: got %q, want busy", ev.Type)
	}
	waitForHook(t, h, "on-activity", "pane-1", afterGen)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("on-activity hook marker missing after wait-hook: %v", err)
	}
}

func TestHookReceivesEnvVars(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "hook-env")
	after := strings.TrimSpace(h.runCmd("hook-gen"))
	afterGen, err := strconv.ParseUint(after, 10, 64)
	if err != nil {
		t.Fatalf("parse hook-gen: %v (output %q)", err, after)
	}

	h.runCmd("set-hook", "on-idle", "env > "+envFile)

	h.sendKeys("pane-1", "echo ENV_TEST", "Enter")
	h.waitFor("pane-1", "ENV_TEST")
	waitForHook(t, h, "on-idle", "pane-1", afterGen)
	data, err := os.ReadFile(envFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("hook env output missing after wait-hook: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "AMUX_PANE_ID=") {
		t.Errorf("missing AMUX_PANE_ID in hook env")
	}
	if !strings.Contains(content, "AMUX_PANE_NAME=pane-1") {
		t.Errorf("missing AMUX_PANE_NAME=pane-1 in hook env")
	}
	if !strings.Contains(content, "AMUX_EVENT=on-idle") {
		t.Errorf("missing AMUX_EVENT=on-idle in hook env")
	}
}

func TestHookFailingCommandLogsToSessionLog(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)
	after := strings.TrimSpace(h.runCmd("hook-gen"))
	afterGen, err := strconv.ParseUint(after, 10, 64)
	if err != nil {
		t.Fatalf("parse hook-gen: %v (output %q)", err, after)
	}

	h.runCmd("set-hook", "on-idle", "/nonexistent/binary/xyz")

	h.sendKeys("pane-1", "echo FAIL_TEST", "Enter")
	h.waitFor("pane-1", "FAIL_TEST")
	waitForHook(t, h, "on-idle", "pane-1", afterGen)
	logPath := filepath.Join(server.SocketDir(), h.session+".log")
	contentBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading session log after wait-hook: %v", err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, "hook") || !strings.Contains(content, "failed") {
		t.Fatalf("expected hook failure in session log, got:\n%s", content)
	}
}
