package test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeMailboxRewakeHookExitsCleanlyWithoutPane(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", mailboxRewakeHookEnv(t, tempDir,
		"AMUX_PANE=",
	))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("output = %q, want none", out)
	}
}

func TestClaudeMailboxRewakeHookFailsOpenWhenAmuxMissing(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", mailboxRewakeHookEnv(t, tempDir,
		"PATH="+tempDir,
	))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("output = %q, want none", out)
	}
}

func TestClaudeMailboxRewakeHookFailsOpenOnMalformedDrainStatus(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeMailboxRewakeFakeCommands(t, tempDir, []string{`not-json`})
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", mailboxRewakeHookEnv(t, tempDir))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", exitCode, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("output = %q, want none", out)
	}
}

func TestClaudeMailboxRewakeHookUsesLockToAvoidDuplicateWatchers(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeMailboxRewakeFakeCommands(t, tempDir, []string{
		`{"pending":0,"pending_ids":[],"pending_fingerprint":"","latest":[]}`,
		`{"pending":1,"pending_ids":["msg-000002"],"pending_fingerprint":"fp-new","latest":[]}`,
	})
	logPath := filepath.Join(tempDir, "amux.log")
	env := mailboxRewakeHookEnv(t, tempDir,
		"FAKE_AMUX_LOG="+logPath,
		"FAKE_AMUX_WAIT_SLEEP=0.2",
	)

	first := startMailboxRewakeHook(t, env)
	waitForFileContains(t, logPath, "wait msg pane-2")

	secondOut, secondExit := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", env)
	if secondExit != 0 {
		t.Fatalf("second exit code = %d, want 0\n%s", secondExit, secondOut)
	}
	if strings.TrimSpace(secondOut) != "" {
		t.Fatalf("second output = %q, want none", secondOut)
	}

	firstOut, firstExit := waitMailboxRewakeHook(t, first)
	if firstExit != 2 {
		t.Fatalf("first exit code = %d, want wake exit 2\n%s", firstExit, firstOut)
	}

	gotLog := readTrimmedFile(t, logPath)
	if got := strings.Count(gotLog, "wait msg pane-2"); got != 1 {
		t.Fatalf("wait watcher count = %d, want 1\n%s", got, gotLog)
	}
}

func TestClaudeMailboxRewakeHookDedupesPendingFingerprint(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeMailboxRewakeFakeCommands(t, tempDir, []string{
		`{"pending":0,"pending_ids":[],"pending_fingerprint":"","latest":[]}`,
		`{"pending":1,"pending_ids":["msg-000002"],"pending_fingerprint":"fp-same","latest":[]}`,
		`{"pending":0,"pending_ids":[],"pending_fingerprint":"","latest":[]}`,
		`{"pending":1,"pending_ids":["msg-000002"],"pending_fingerprint":"fp-same","latest":[]}`,
	})
	env := mailboxRewakeHookEnv(t, tempDir)

	firstOut, firstExit := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", env)
	if firstExit != 2 {
		t.Fatalf("first exit code = %d, want wake exit 2\n%s", firstExit, firstOut)
	}

	secondOut, secondExit := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", env)
	if secondExit != 0 {
		t.Fatalf("second exit code = %d, want deduped exit 0\n%s", secondExit, secondOut)
	}
	if strings.TrimSpace(secondOut) != "" {
		t.Fatalf("second output = %q, want none", secondOut)
	}
}

func TestClaudeMailboxRewakeHookOutputIsBoundedAndBodyFree(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeMailboxRewakeFakeCommands(t, tempDir, []string{
		`{"pending":0,"pending_ids":[],"pending_fingerprint":"","latest":[]}`,
		`{"pending":1,"pending_ids":["msg-000002"],"pending_fingerprint":"fp-secret","latest":[{"id":"msg-000002","subject":"SECRET SUBJECT","body":"SECRET BODY","metadata":{"token":"SECRET TOKEN"}}]}`,
	})
	out, exitCode := runBashScriptWithInput(t, ".claude/hooks/mailbox-rewake.sh", "", mailboxRewakeHookEnv(t, tempDir))
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want wake exit 2\n%s", exitCode, out)
	}
	if len(out) > 700 {
		t.Fatalf("output length = %d, want bounded output:\n%s", len(out), out)
	}
	for _, leaked := range []string{"SECRET SUBJECT", "SECRET BODY", "SECRET TOKEN", "msg-000002"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("wake output leaked %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{
		"amux msg drain-status --format json",
		"amux msg read <id> --for pane-2",
		"amux msg ack <id> --for pane-2 --status seen",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("wake output missing %q:\n%s", want, out)
		}
	}
}

func TestCodexMailboxRewakeHookEmitsBlockJSON(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	writeMailboxRewakeFakeCommands(t, tempDir, []string{
		`{"pending":0,"pending_ids":[],"pending_fingerprint":"","latest":[]}`,
		`{"pending":1,"pending_ids":["msg-000002"],"pending_fingerprint":"fp-secret","latest":[{"id":"msg-000002","subject":"SECRET SUBJECT","body":"SECRET BODY","metadata":{"token":"SECRET TOKEN"}}]}`,
	})
	out, exitCode := runBashScriptWithInput(t, ".codex/hooks/amux-mailbox-rewake.sh", "", mailboxRewakeHookEnv(t, tempDir))
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want Codex block JSON exit 0\n%s", exitCode, out)
	}

	var block struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &block); err != nil {
		t.Fatalf("unmarshal Codex rewake output: %v\n%s", err, out)
	}
	if block.Decision != "block" {
		t.Fatalf("decision = %q, want block\n%s", block.Decision, out)
	}
	for _, want := range []string{
		"amux msg drain-status --format json",
		"amux msg read <id> --for pane-2",
		"amux msg ack <id> --for pane-2 --status seen",
	} {
		if !strings.Contains(block.Reason, want) {
			t.Fatalf("Codex rewake reason missing %q:\n%s", want, block.Reason)
		}
	}
	for _, leaked := range []string{"SECRET SUBJECT", "SECRET BODY", "SECRET TOKEN", "msg-000002"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("Codex rewake output leaked %q:\n%s", leaked, out)
		}
	}
}

func TestClaudeSettingsWiresMailboxRewakeAsyncHook(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(repoPath(t, ".claude/settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type         string `json:"type"`
				Command      string `json:"command"`
				AsyncRewake bool   `json:"asyncRewake"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	for _, group := range settings.Hooks["Stop"] {
		for _, hook := range group.Hooks {
			if hook.Command == ".claude/hooks/mailbox-rewake.sh" {
				if hook.Type != "command" {
					t.Fatalf("mailbox rewake hook type = %q, want command", hook.Type)
				}
				if !hook.AsyncRewake {
					t.Fatal("mailbox rewake hook missing asyncRewake=true")
				}
				return
			}
		}
	}
	t.Fatal("missing .claude/hooks/mailbox-rewake.sh asyncRewake Stop hook")
}

func mailboxRewakeHookEnv(t *testing.T, tempDir string, extra ...string) []string {
	t.Helper()

	home := filepath.Join(tempDir, "home")
	state := filepath.Join(tempDir, "state")
	socketDir := filepath.Join(tempDir, "sockets")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(socketDir, "rewake-test"), []byte("socket identity"), 0o600); err != nil {
		t.Fatalf("write fake socket: %v", err)
	}

	env := append([]string{}, hermeticMainEnv()...)
	env = upsertIssueMetaEnv(env, "PATH", tempDir+string(os.PathListSeparator)+issueMetaEnvValue(env, "PATH"))
	env = upsertIssueMetaEnv(env, "HOME", home)
	env = upsertIssueMetaEnv(env, "XDG_STATE_HOME", state)
	env = append(env,
		"AMUX_SESSION=rewake-test",
		"AMUX_PANE=pane-2",
		"AMUX_SOCKET_DIR="+socketDir,
		"AMUX_MAILBOX_DRAIN_TIMEOUT=1s",
		"AMUX_MAILBOX_REWAKE_WAIT_TIMEOUT=1s",
	)
	return append(env, extra...)
}

func writeMailboxRewakeFakeCommands(t *testing.T, dir string, statuses []string) {
	t.Helper()

	if len(statuses) == 0 {
		t.Fatal("statuses must not be empty")
	}
	statusPath := filepath.Join(dir, "statuses.ndjson")
	if err := os.WriteFile(statusPath, []byte(strings.Join(statuses, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write statuses: %v", err)
	}
	cursorPath := filepath.Join(dir, "status.cursor")

	writeRewakeExecutable(t, filepath.Join(dir, "timeout"), `#!/bin/sh
shift
exec "$@"
`)
	writeRewakeExecutable(t, filepath.Join(dir, "gtimeout"), `#!/bin/sh
shift
exec "$@"
`)
	writeRewakeExecutable(t, filepath.Join(dir, "amux"), `#!/bin/sh
if [ -n "${FAKE_AMUX_LOG:-}" ]; then
    printf '%s' "$1" >>"$FAKE_AMUX_LOG"
    shift
    for arg in "$@"; do
        printf ' %s' "$arg" >>"$FAKE_AMUX_LOG"
    done
    printf '\n' >>"$FAKE_AMUX_LOG"
    set -- $(tail -n 1 "$FAKE_AMUX_LOG")
fi

if [ "$1" = "msg" ] && [ "$2" = "drain-status" ]; then
    n="$(cat "`+cursorPath+`" 2>/dev/null || printf '1')"
    sed -n "${n}p" "`+statusPath+`"
    total="$(wc -l <"`+statusPath+`" | tr -d '[:space:]')"
    if [ "$n" -lt "$total" ]; then
        printf '%s\n' "$((n + 1))" >"`+cursorPath+`"
    fi
    exit 0
fi

if [ "$1" = "wait" ] && [ "$2" = "msg" ]; then
    if [ -n "${FAKE_AMUX_WAIT_SLEEP:-}" ]; then
        sleep "$FAKE_AMUX_WAIT_SLEEP"
    fi
    printf '%s\n' "${FAKE_AMUX_WAIT_OUTPUT:-{\"id\":\"msg-000002\"}}"
    exit "${FAKE_AMUX_WAIT_EXIT:-0}"
fi

printf 'unexpected amux args:' >&2
for arg in "$@"; do
    printf ' %s' "$arg" >&2
done
printf '\n' >&2
exit 1
`)
}

func writeRewakeExecutable(t *testing.T, path, script string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type mailboxRewakeProcess struct {
	cmd *exec.Cmd
	out bytes.Buffer
}

func startMailboxRewakeHook(t *testing.T, env []string) *mailboxRewakeProcess {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, "bash", repoPath(t, ".claude/hooks/mailbox-rewake.sh"))
	cmd.Dir = repoRoot(t)
	cmd.Env = env
	proc := &mailboxRewakeProcess{cmd: cmd}
	cmd.Stdout = &proc.out
	cmd.Stderr = &proc.out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mailbox rewake hook: %v", err)
	}
	return proc
}

func waitMailboxRewakeHook(t *testing.T, proc *mailboxRewakeProcess) (string, int) {
	t.Helper()

	err := proc.cmd.Wait()
	out := proc.out.String()
	if err == nil {
		return out, 0
	}
	exitErr := mustExitError(t, err, []byte(out))
	return out, exitErr.ExitCode()
}

func waitForFileContains(t *testing.T, path, want string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("timed out waiting for %q in %s:\n%s", want, path, data)
}
