package main

import (
	"slices"
	"strings"
	"testing"
	"time"
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
	if !strings.Contains(out, "usage: amux send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...") {
		t.Fatalf("usage output missing via/wait flags:\n%s", out)
	}
}

func TestMainKeyCommandsHelpFlagsPrintUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "send-keys long help",
			args: []string{"send-keys", "pane-1", "--help"},
			want: "usage: amux send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...",
		},
		{
			name: "send-keys short help",
			args: []string{"send-keys", "pane-1", "-h"},
			want: "usage: amux send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("usage output = %q, want substring %q", out, tt.want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("help flag should not dispatch to the server:\n%s", out)
			}
		})
	}
}

func TestMainWaitUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "wait")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...") {
		t.Fatalf("wait usage output = %q", out)
	}
}

func TestMainMetaCommandsHelpFlagsPrintUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "meta long help",
			args: []string{"meta", "--help"},
			want: metaUsage,
		},
		{
			name: "meta short help",
			args: []string{"meta", "-h"},
			want: metaUsage,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("usage output = %q, want substring %q", out, tt.want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("help flag should not dispatch to the server:\n%s", out)
			}
		})
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

func TestMainRemovedCommandsAreUnknown(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"set-hook", "unset-hook", "list-hooks", "delegate", "attach", "dashboard",
		"type-keys", "split", "add-pane", "connection-log", "pane-log",
		"set-kv", "get-kv", "rm-kv", "set-meta", "add-meta", "rm-meta", "refresh-meta",
		"move-up", "move-down", "move-to", "set-lead", "unset-lead", "toggle-lead", "swap-tree",
	} {
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

func TestMainSendKeysDispatchesWhenKeysProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "send-keys", "pane-1", "abc")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux send-keys") {
		t.Fatalf("send-keys should dispatch when keys are provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "send-keys")
}

func TestMainSendKeysUsageIncludesViaFlags(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "send-keys")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux send-keys <pane> [--via pty|client] [--wait ready|ui=input-idle] [--timeout <duration>] [--delay-final <duration>] [--hex] <keys>...") {
		t.Fatalf("send-keys usage output missing via flags:\n%s", out)
	}
}

func TestMainMoveUsageAndDispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		args            []string
		wantUsage       string
		wantConnectName string
	}{
		{
			name:      "move usage",
			args:      []string{"move"},
			wantUsage: moveUsage,
		},
		{
			name:            "move up dispatch",
			args:            []string{"move", "pane-1", "up"},
			wantConnectName: "move-up",
		},
		{
			name:            "move down dispatch",
			args:            []string{"move", "pane-1", "down"},
			wantConnectName: "move-down",
		},
		{
			name:            "move before dispatch",
			args:            []string{"move", "pane-1", "--before", "pane-2"},
			wantConnectName: "move",
		},
		{
			name:            "move to-column dispatch",
			args:            []string{"move", "pane-1", "--to-column", "pane-2"},
			wantConnectName: "move-to",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, code := runHermeticMain(t, tt.args...)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			if tt.wantUsage != "" {
				if !strings.Contains(out, tt.wantUsage) {
					t.Fatalf("usage output = %q, want substring %q", out, tt.wantUsage)
				}
				return
			}
			if strings.Contains(out, "usage: amux "+tt.wantConnectName) {
				t.Fatalf("%s should dispatch when a pane is provided, got usage output:\n%s", tt.wantConnectName, out)
			}
			assertMainCommandConnectError(t, out, tt.wantConnectName)
		})
	}
}

func TestMainHelpIncludesCanonicalCommands(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "amux [-s session] log clients") {
		t.Fatalf("help output missing log clients:\n%s", out)
	}
	if !strings.Contains(out, "amux [-s session] meta set <pane> key=value [key=value...]") {
		t.Fatalf("help output missing meta set:\n%s", out)
	}
	if !strings.Contains(out, "amux [-s session] lead [pane]") {
		t.Fatalf("help output missing lead:\n%s", out)
	}
	if !strings.Contains(out, "amux [-s session] respawn <pane>") {
		t.Fatalf("help output missing respawn:\n%s", out)
	}
	if strings.Contains(out, "amux [-s session] attach [session]") {
		t.Fatalf("help output should omit removed attach alias:\n%s", out)
	}
	if strings.Contains(out, "amux [-s session] move-up <pane>") || strings.Contains(out, "amux [-s session] move-down <pane>") {
		t.Fatalf("help output should omit removed move aliases:\n%s", out)
	}
}

func TestMainHelpOmitsDelegate(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if strings.Contains(out, "amux [-s session] delegate <pane>") {
		t.Fatalf("help output should omit delegate:\n%s", out)
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

func TestMainHelpDescribesCtrlASpiralSpawn(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "Ctrl-a a                           Spawn pane in clockwise spiral order") {
		t.Fatalf("help output missing updated Ctrl-a a text:\n%s", out)
	}
	if strings.Contains(out, "Ctrl-a a                           Add pane in clockwise spiral order") {
		t.Fatalf("help output should not mention add-pane for Ctrl-a a:\n%s", out)
	}
}

func TestMainAllCommandsSupportLongHelp(t *testing.T) {
	t.Parallel()

	for _, command := range mainDispatchCommands(t) {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			out, code, timedOut := runHermeticMainWithTimeout(t, 2*time.Second, command, "--help")
			if timedOut {
				t.Fatalf("%s --help timed out\n%s", command, out)
			}
			if code != 0 {
				t.Fatalf("%s --help exit code = %d, want 0\n%s", command, code, out)
			}
			want := "usage: amux " + command
			if !strings.Contains(out, want) {
				t.Fatalf("%s --help output = %q, want substring %q", command, out, want)
			}
			if strings.Contains(out, "connecting to server") {
				t.Fatalf("%s --help should not dispatch to the server:\n%s", command, out)
			}
		})
	}
}

func mainDispatchCommands(t *testing.T) []string {
	t.Helper()

	commands := map[string]struct{}{}
	for name := range commandUsageByName {
		commands[name] = struct{}{}
	}

	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
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

func TestMainRespawnUsage(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "respawn")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "usage: amux respawn <pane>") {
		t.Fatalf("respawn usage output = %q", out)
	}
}

func TestMainRespawnDispatchesWhenPaneProvided(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "respawn", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux respawn") {
		t.Fatalf("respawn should dispatch when a pane is provided, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "respawn")
}
func TestMainMetaDispatchesWhenSubcommandIsValid(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "meta", "get", "pane-1")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "usage: amux meta") {
		t.Fatalf("meta should dispatch when subcommand args are valid, got usage output:\n%s", out)
	}
	assertMainCommandConnectError(t, out, "meta")
}

func TestMainMetaUsageRejectsMissingArgs(t *testing.T) {
	t.Parallel()

	out, code := runHermeticMain(t, "meta")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, metaUsage) {
		t.Fatalf("meta usage output = %q", out)
	}
}
