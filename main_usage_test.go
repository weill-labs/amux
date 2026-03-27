package main

import (
	"strings"
	"testing"
)

func TestMainUsageHelperIsolatesAmbientSessionEnv(t *testing.T) {
	t.Parallel()

	cmd := newHermeticMainCmd(t, "kill")

	if !strings.Contains(strings.Join(cmd.Args, "\x00"), "\x00-s\x00"+hermeticMainSession(t.Name())+"\x00kill") {
		t.Fatalf("helper args = %q, want injected isolated session before command", cmd.Args)
	}
	if got, want := strings.Join(cmd.Env, "\x00"), strings.Join(hermeticMainEnv(), "\x00"); got != want {
		t.Fatalf("helper env = %q, want %q", cmd.Env, hermeticMainEnv())
	}
	for _, prefix := range []string{"AMUX_PANE=", "AMUX_SESSION=", "TMUX="} {
		for _, entry := range cmd.Env {
			if strings.HasPrefix(entry, prefix) {
				t.Fatalf("helper env leaked %s in %q", prefix, entry)
			}
		}
	}
}

func TestMainSendKeysUsageIncludesWaitReadyFlags(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "send-keys", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux send-keys <pane> [--wait ready] [--continue-known-dialogs] [--timeout <duration>] [--hex] <keys>...") {
		t.Fatalf("usage output missing wait-ready flags:\n%s", out)
	}
}

func TestMainWaitUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "wait")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux wait <idle|busy|vt-idle|ready|content|layout|clipboard|checkpoint|ui> ...") {
		t.Fatalf("wait usage output = %q", out)
	}
}

func TestMainCursorUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "cursor")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux cursor <layout|clipboard|ui> [--client <id>]") {
		t.Fatalf("cursor usage output = %q", out)
	}
}

func TestMainRemovedHookCommandsAreUnknown(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"set-hook", "unset-hook", "list-hooks"} {
		out, code := runHermeticMain(t, command)
		if code != 1 {
			t.Fatalf("%s exit code = %d, want 1\n%s", command, code, out)
		}
		if !strings.Contains(out, "amux: unknown command \""+command+"\"") {
			t.Fatalf("%s output = %q", command, out)
		}
	}
}

func TestMainCopyModeDispatchesWithoutExplicitPane(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "copy-mode")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux copy-mode") {
		t.Fatalf("copy-mode should dispatch without a pane argument, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "copy-mode")
}

func TestMainCursorDispatchesWhenKindProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "cursor", "layout")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux cursor") {
		t.Fatalf("cursor should dispatch when a kind is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "cursor")
}

func TestMainWaitDispatchesWhenKindProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "wait", "layout")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux wait") {
		t.Fatalf("wait should dispatch when a kind is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "wait")
}

func TestMainTypeKeysDispatchesWhenKeysProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "type-keys", "abc")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux type-keys") {
		t.Fatalf("type-keys should dispatch when keys are provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "type-keys")
}

func TestMainTypeKeysUsageIncludesWaitFlags(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "type-keys")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux type-keys [--wait ui=input-idle] [--timeout <duration>] [--hex] <keys>...") {
		t.Fatalf("type-keys usage output missing wait flags:\n%s", out)
	}
}

func TestMainHelpIncludesWaitCheckpoint(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "amux [-s session] wait checkpoint [--after N] [--timeout 15s]") {
		t.Fatalf("help output missing wait-checkpoint:\n%s", out)
	}
}

func TestMainKillAllowsImplicitActivePane(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "kill")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux kill") {
		t.Fatalf("kill should accept an omitted pane, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "kill")
}

func TestMainKillUsageRejectsTimeoutWithoutCleanup(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "kill", "--timeout", "1s")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux kill [--cleanup] [--timeout <duration>] [pane]") {
		t.Fatalf("kill usage output = %q", out)
	}
}

func TestMainResetUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "reset")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux reset <pane>") {
		t.Fatalf("reset usage output = %q", out)
	}
}

func TestMainRefreshMetaDispatchesWithoutExplicitPane(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "refresh-meta")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux refresh-meta") {
		t.Fatalf("refresh-meta should dispatch without a pane argument, got usage output:\n%s", out)
	}
	if !strings.Contains(out, "server not running") {
		t.Fatalf("refresh-meta should attempt the command, got:\n%s", out)
	}
}

func TestMainRefreshMetaUsageRejectsExtraArgs(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "refresh-meta", "pane-1", "extra")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux refresh-meta [pane]") {
		t.Fatalf("refresh-meta usage output = %q", out)
	}
}
