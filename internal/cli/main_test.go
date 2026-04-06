package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
)

func TestParseSpawnCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantCmd     string
		wantArgs    []string
		wantErrText string
	}{
		{name: "default spawn", args: nil, wantCmd: "spawn", wantArgs: []string{}},
		{name: "focused spawn", args: []string{"--focus"}, wantCmd: "spawn", wantArgs: []string{"--focus"}},
		{name: "split at pane", args: []string{"--at", "pane-1"}, wantCmd: "split", wantArgs: []string{"pane-1"}},
		{name: "split active vertical", args: []string{"--vertical"}, wantCmd: "split", wantArgs: []string{"v"}},
		{name: "split root vertical", args: []string{"--at", "pane-1", "--root", "--vertical"}, wantCmd: "split", wantArgs: []string{"pane-1", "root", "v"}},
		{name: "split with metadata", args: []string{"--at", "pane-1", "--task", "build", "--color", "blue"}, wantCmd: "split", wantArgs: []string{"pane-1", "--task", "build", "--color", "blue"}},
		{name: "conflicting directions", args: []string{"--vertical", "--horizontal"}, wantErrText: spawnUsage},
		{name: "spiral rejected", args: []string{"--spiral"}, wantErrText: spawnUsage},
		{name: "missing at value", args: []string{"--at"}, wantErrText: spawnUsage},
		{name: "unknown arg", args: []string{"pane-1"}, wantErrText: spawnUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCmd, gotArgs, err := ParseSpawnCommandArgs(tt.args)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("ParseSpawnCommandArgs(%v): expected error containing %q", tt.args, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("ParseSpawnCommandArgs(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpawnCommandArgs(%v): unexpected error: %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("ParseSpawnCommandArgs(%v) cmd = %q, want %q", tt.args, gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("ParseSpawnCommandArgs(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestResolveSessionName(t *testing.T) {
	tests := []struct {
		name        string
		explicit    string
		explicitSet bool
		envSession  string
		want        string
	}{
		{name: "default when unset", want: DefaultSessionName},
		{name: "use AMUX_SESSION when flag omitted", envSession: "current-session", want: "current-session"},
		{name: "explicit session without env", explicit: "other-session", explicitSet: true, want: "other-session"},
		{name: "explicit session beats AMUX_SESSION", explicit: "other-session", explicitSet: true, envSession: "current-session", want: "other-session"},
		{name: "explicit empty session still beats AMUX_SESSION", explicit: "", explicitSet: true, envSession: "current-session", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AMUX_SESSION", tt.envSession)

			if got := ResolveSessionName(tt.explicit, tt.explicitSet); got != tt.want {
				t.Fatalf("ResolveSessionName(%q, %t) = %q, want %q", tt.explicit, tt.explicitSet, got, tt.want)
			}
		})
	}
}

func TestDefaultSessionNameValue(t *testing.T) {
	t.Parallel()

	if DefaultSessionName != "main" {
		t.Fatalf("DefaultSessionName = %q, want %q", DefaultSessionName, "main")
	}
}

func TestBuildVersionIncludesCheckpointVersion(t *testing.T) {
	t.Parallel()

	got := buildVersion("")
	want := "checkpoint v" + strconv.Itoa(checkpoint.ServerCheckpointVersion)
	if !strings.Contains(got, want) {
		t.Fatalf("buildVersion(\"\") = %q, want substring %q", got, want)
	}
}

func TestWriteVersionOutputJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersionOutput("", &out, []string{"--json"}); err != nil {
		t.Fatalf("writeVersionOutput(\"\", --json): %v", err)
	}

	var info struct {
		Build             string `json:"build"`
		CheckpointVersion int    `json:"checkpoint_version"`
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json.Unmarshal(version output): %v\nraw:\n%s", err, out.String())
	}
	if info.Build == "" {
		t.Fatal("version json build = empty, want build identifier")
	}
	if info.CheckpointVersion != checkpoint.ServerCheckpointVersion {
		t.Fatalf("checkpoint_version = %d, want %d", info.CheckpointVersion, checkpoint.ServerCheckpointVersion)
	}
}

func TestWriteVersionOutputHash(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersionOutput("", &out, []string{"--hash"}); err != nil {
		t.Fatalf("writeVersionOutput(\"\", --hash): %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != buildHash("") {
		t.Fatalf("hash output = %q, want %q", got, buildHash(""))
	}
}

func TestResolveInvocationSession(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		envSession string
		wantName   string
		wantArgs   []string
	}{
		{name: "default when unset", args: []string{"status"}, wantName: DefaultSessionName, wantArgs: []string{"status"}},
		{name: "uses AMUX_SESSION when flag omitted", args: []string{"status"}, envSession: "current-session", wantName: "current-session", wantArgs: []string{"status"}},
		{name: "strips explicit session flag", args: []string{"-s", "other-session", "status"}, envSession: "current-session", wantName: "other-session", wantArgs: []string{"status"}},
		{name: "strips explicit session flag from middle", args: []string{"events", "-s", "other-session", "--no-reconnect"}, wantName: "other-session", wantArgs: []string{"events", "--no-reconnect"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AMUX_SESSION", tt.envSession)

			gotName, gotArgs := ResolveInvocationSession(tt.args)
			if gotName != tt.wantName {
				t.Fatalf("ResolveInvocationSession(%v) session = %q, want %q", tt.args, gotName, tt.wantName)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("ResolveInvocationSession(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestResolveCanonicalSessionCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantCmd     string
		wantArgs    []string
		wantHandled bool
		wantErrText string
	}{
		{
			name:        "unknown command falls through",
			args:        []string{"spawn"},
			wantHandled: false,
		},
		{
			name:        "status ignores extra args",
			args:        []string{"status", "ignored"},
			wantCmd:     "status",
			wantHandled: true,
		},
		{
			name:        "last-window uses no-arg session command",
			args:        []string{"last-window"},
			wantCmd:     "last-window",
			wantHandled: true,
		},
		{
			name:        "list forwards args",
			args:        []string{"list", "--no-cwd"},
			wantCmd:     "list",
			wantArgs:    []string{"--no-cwd"},
			wantHandled: true,
		},
		{
			name:        "cursor forwards args after minimum",
			args:        []string{"cursor", "layout"},
			wantCmd:     "cursor",
			wantArgs:    []string{"layout"},
			wantHandled: true,
		},
		{
			name:        "reset narrows to pane arg",
			args:        []string{"reset", "pane-1", "ignored"},
			wantCmd:     "reset",
			wantArgs:    []string{"pane-1"},
			wantHandled: true,
		},
		{
			name:        "respawn narrows to pane arg",
			args:        []string{"respawn", "pane-1", "ignored"},
			wantCmd:     "respawn",
			wantArgs:    []string{"pane-1"},
			wantHandled: true,
		},
		{
			name:        "wait needs a kind",
			args:        []string{"wait"},
			wantHandled: true,
			wantErrText: "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...",
		},
		{
			name:        "unsplice needs a host",
			args:        []string{"unsplice"},
			wantHandled: true,
			wantErrText: "usage: amux unsplice <host>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCmd, gotArgs, gotHandled, err := ResolveCanonicalSessionCommand(tt.args)
			if gotHandled != tt.wantHandled {
				t.Fatalf("ResolveCanonicalSessionCommand(%v) handled = %v, want %v", tt.args, gotHandled, tt.wantHandled)
			}
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("ResolveCanonicalSessionCommand(%v): expected error containing %q", tt.args, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("ResolveCanonicalSessionCommand(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveCanonicalSessionCommand(%v): unexpected error: %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("ResolveCanonicalSessionCommand(%v) cmd = %q, want %q", tt.args, gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("ResolveCanonicalSessionCommand(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
			}
		})
	}
}

func TestMaybePrintKeyCommandUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		usage        string
		minArgs      int
		wantHandled  bool
		wantExitCode int
		wantStdout   string
		wantStderr   string
	}{
		{
			name:         "send-keys long help after pane",
			args:         []string{"pane-1", "--help"},
			usage:        sendKeysUsage,
			minArgs:      2,
			wantHandled:  true,
			wantExitCode: 0,
			wantStdout:   sendKeysUsage + "\n",
		},
		{
			name:         "send-keys short help after flag scan",
			args:         []string{"pane-1", "--hex", "-h"},
			usage:        sendKeysUsage,
			minArgs:      2,
			wantHandled:  true,
			wantExitCode: 0,
			wantStdout:   sendKeysUsage + "\n",
		},
		{
			name:         "send-keys usage error",
			args:         []string{"pane-1"},
			usage:        sendKeysUsage,
			minArgs:      2,
			wantHandled:  true,
			wantExitCode: 1,
			wantStderr:   sendKeysUsage + "\n",
		},
		{
			name:         "send-keys dispatch with flags and keys",
			args:         []string{"pane-1", "--hex", "6162"},
			usage:        sendKeysUsage,
			minArgs:      2,
			wantHandled:  false,
			wantExitCode: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer

			handled, exitCode := MaybePrintKeyCommandUsage(&stdout, &stderr, tt.args, tt.usage, tt.minArgs)
			if handled != tt.wantHandled {
				t.Fatalf("handled = %t, want %t", handled, tt.wantHandled)
			}
			if exitCode != tt.wantExitCode {
				t.Fatalf("exitCode = %d, want %d", exitCode, tt.wantExitCode)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
			if got := stderr.String(); got != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func TestParseSwapArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
		wantErr  string
	}{
		{name: "forward", args: []string{"forward"}, wantCmd: "swap", wantArgs: []string{"forward"}},
		{name: "pair", args: []string{"pane-1", "pane-2"}, wantCmd: "swap", wantArgs: []string{"pane-1", "pane-2"}},
		{name: "tree", args: []string{"pane-1", "pane-2", "--tree"}, wantCmd: "swap-tree", wantArgs: []string{"pane-1", "pane-2"}},
		{name: "missing args", args: nil, wantErr: swapUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotArgs, err := ParseSwapArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseSwapArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSwapArgs(%v): %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd || !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("parseSwapArgs(%v) = (%q, %v), want (%q, %v)", tt.args, gotCmd, gotArgs, tt.wantCmd, tt.wantArgs)
			}
		})
	}
}

func TestParseMoveArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
		wantErr  string
	}{
		{name: "up", args: []string{"pane-1", "up"}, wantCmd: "move-up", wantArgs: []string{"pane-1"}},
		{name: "down", args: []string{"pane-1", "down"}, wantCmd: "move-down", wantArgs: []string{"pane-1"}},
		{name: "before", args: []string{"pane-1", "--before", "pane-2"}, wantCmd: "move", wantArgs: []string{"pane-1", "--before", "pane-2"}},
		{name: "to column", args: []string{"pane-1", "--to-column", "pane-2"}, wantCmd: "move-to", wantArgs: []string{"pane-1", "pane-2"}},
		{name: "missing args", args: []string{"pane-1"}, wantErr: moveUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotArgs, err := ParseMoveArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseMoveArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMoveArgs(%v): %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd || !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("parseMoveArgs(%v) = (%q, %v), want (%q, %v)", tt.args, gotCmd, gotArgs, tt.wantCmd, tt.wantArgs)
			}
		})
	}
}

func TestParseLeadArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantArgs []string
		wantErr  string
	}{
		{name: "default", wantCmd: "set-lead"},
		{name: "target pane", args: []string{"pane-1"}, wantCmd: "set-lead", wantArgs: []string{"pane-1"}},
		{name: "clear", args: []string{"--clear"}, wantCmd: "unset-lead"},
		{name: "invalid", args: []string{"--toggle"}, wantErr: leadUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCmd, gotArgs, err := ParseLeadArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseLeadArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLeadArgs(%v): %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd || !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("parseLeadArgs(%v) = (%q, %v), want (%q, %v)", tt.args, gotCmd, gotArgs, tt.wantCmd, tt.wantArgs)
			}
		})
	}
}

func TestValidateMetaArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "set", args: []string{"set", "pane-1", "task=build"}},
		{name: "get", args: []string{"get", "pane-1"}},
		{name: "rm", args: []string{"rm", "pane-1", "issue"}},
		{name: "missing", wantErr: metaUsage},
		{name: "unknown", args: []string{"sync"}, wantErr: metaUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMetaArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateMetaArgs(%v) error = %v, want %q", tt.args, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMetaArgs(%v): %v", tt.args, err)
			}
		})
	}
}

func TestMaybePrintCommandHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantHandled bool
		wantStdout  string
	}{
		{
			name:        "recognized long help as first arg",
			args:        []string{"new-window", "--help"},
			wantHandled: true,
			wantStdout:  "usage: amux new-window [--name NAME]\n",
		},
		{
			name:        "recognized short help as first arg",
			args:        []string{"status", "-h"},
			wantHandled: true,
			wantStdout:  "usage: amux status\n",
		},
		{
			name:        "equalize long help as first arg",
			args:        []string{"equalize", "--help"},
			wantHandled: true,
			wantStdout:  "usage: amux equalize [--vertical|--all]\n",
		},
		{
			name:        "debug long help as first arg",
			args:        []string{"debug", "--help"},
			wantHandled: true,
			wantStdout:  debugUsage + "\n",
		},
		{
			name:        "wait long help reflects idle rename",
			args:        []string{"wait", "--help"},
			wantHandled: true,
			wantStdout:  "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...\n",
		},
		{
			name:        "last-window help",
			args:        []string{"last-window", "--help"},
			wantHandled: true,
			wantStdout:  "usage: amux last-window\n",
		},
		{
			name:        "help after command args stays unhandled",
			args:        []string{"send-keys", "pane-1", "--help"},
			wantHandled: false,
		},
		{
			name:        "unknown command stays unhandled",
			args:        []string{"unknown", "--help"},
			wantHandled: false,
		},
		{
			name:        "missing help flag stays unhandled",
			args:        []string{"rotate", "--reverse"},
			wantHandled: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer

			handled := MaybePrintCommandHelp(&stdout, tt.args)
			if handled != tt.wantHandled {
				t.Fatalf("handled = %t, want %t", handled, tt.wantHandled)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

func TestPrintUsageOmitsDelegate(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	WriteUsage(&buf)

	if strings.Contains(buf.String(), "amux [-s session] delegate <pane>") {
		t.Fatalf("printUsage should omit delegate:\n%s", buf.String())
	}
}

func TestRunMainDefaultSession(t *testing.T) {
	t.Run("attaches to resolved session", func(t *testing.T) {
		t.Setenv("AMUX_CHECKPOINT", "")

		h := newCLIRuntimeHarness()
		if exitCode := RunWithRuntime(nil, h.runtime()); exitCode != 0 {
			t.Fatalf("RunWithRuntime() exit = %d, want 0", exitCode)
		}

		wantSession := ResolveSessionName("", false)
		want := []cliCall{
			{kind: "check-nesting", session: wantSession},
			{kind: "attach", session: wantSession},
		}
		if !reflect.DeepEqual(h.calls, want) {
			t.Fatalf("calls = %#v, want %#v", h.calls, want)
		}
	})

	t.Run("uses takeover when available", func(t *testing.T) {
		t.Setenv("AMUX_CHECKPOINT", "")

		h := newCLIRuntimeHarness()
		h.shouldTakeover = true
		h.tryTakeoverResult = true

		if exitCode := RunWithRuntime(nil, h.runtime()); exitCode != 0 {
			t.Fatalf("RunWithRuntime() exit = %d, want 0", exitCode)
		}

		want := []cliCall{{kind: "try-takeover", session: ResolveSessionName("", false)}}
		if !reflect.DeepEqual(h.calls, want) {
			t.Fatalf("calls = %#v, want %#v", h.calls, want)
		}
	})
}

func TestRunMainDispatchesCommands(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		env        map[string]string
		wantExit   int
		wantCalls  []cliCall
		wantStdout string
		wantStderr string
	}{
		{
			name:     "checkpoint env starts server",
			env:      map[string]string{"AMUX_CHECKPOINT": "/tmp/checkpoint"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "run-server", session: resolvedSessionMarker},
			},
		},
		{
			name:       "version dispatch",
			args:       []string{"version", "--hash"},
			wantExit:   0,
			wantStdout: "version\n",
			wantCalls: []cliCall{
				{kind: "version", args: []string{"--hash"}},
			},
		},
		{
			name:     "spawn focus dispatches parsed command",
			args:     []string{"spawn", "--focus"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "server-command", session: resolvedSessionMarker, cmd: "spawn", args: []string{"--focus"}},
			},
		},
		{
			name:     "window command honors explicit session",
			args:     []string{"-s", "demo", "select-window", "2"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "server-command", session: "demo", cmd: "select-window", args: []string{"2"}},
			},
		},
		{
			name:     "last-window dispatches through server",
			args:     []string{"last-window"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "server-command", session: resolvedSessionMarker, cmd: "last-window"},
			},
		},
		{
			name:     "events command uses streaming runner",
			args:     []string{"events", "--no-reconnect"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "events", session: resolvedSessionMarker, args: []string{"--no-reconnect"}},
			},
		},
		{
			name:     "debug command uses dedicated runner",
			args:     []string{"debug", "goroutines"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "debug", session: resolvedSessionMarker, args: []string{"goroutines"}},
			},
		},
		{
			name:     "remote command dispatches through server",
			args:     []string{"disconnect", "host-a"},
			wantExit: 0,
			wantCalls: []cliCall{
				{kind: "server-command", session: resolvedSessionMarker, cmd: "disconnect", args: []string{"host-a"}},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AMUX_CHECKPOINT", "")
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			h := newCLIRuntimeHarness()
			if exitCode := RunWithRuntime(tt.args, h.runtime()); exitCode != tt.wantExit {
				t.Fatalf("RunWithRuntime(%v) exit = %d, want %d", tt.args, exitCode, tt.wantExit)
			}
			wantCalls := resolveTestSessions(tt.wantCalls)
			if !reflect.DeepEqual(h.calls, wantCalls) {
				t.Fatalf("calls = %#v, want %#v", h.calls, wantCalls)
			}
			if got := h.stdout.String(); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
			if got := h.stderr.String(); got != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

func TestRunMainHelpAndUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		args           []string
		wantExit       int
		wantUsageCalls int
		wantStderr     string
	}{
		{
			name:           "help command prints usage",
			args:           []string{"help"},
			wantExit:       0,
			wantUsageCalls: 1,
		},
		{
			name:           "unknown command prints usage and error",
			args:           []string{"bogus"},
			wantExit:       1,
			wantUsageCalls: 1,
			wantStderr:     "amux: unknown command \"bogus\"\n",
		},
		{
			name:           "removed dashboard alias is unknown",
			args:           []string{"dashboard"},
			wantExit:       1,
			wantUsageCalls: 1,
			wantStderr:     "amux: unknown command \"dashboard\"\n",
		},
		{
			name:       "send-keys usage error stays in dispatch layer",
			args:       []string{"send-keys", "pane-1"},
			wantExit:   1,
			wantStderr: sendKeysUsage + "\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newCLIRuntimeHarness()
			if exitCode := RunWithRuntime(tt.args, h.runtime()); exitCode != tt.wantExit {
				t.Fatalf("RunWithRuntime(%v) exit = %d, want %d", tt.args, exitCode, tt.wantExit)
			}
			if h.usageCalls != tt.wantUsageCalls {
				t.Fatalf("usageCalls = %d, want %d", h.usageCalls, tt.wantUsageCalls)
			}
			if got := h.stderr.String(); got != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tt.wantStderr)
			}
		})
	}
}

type cliCall struct {
	kind    string
	session string
	cmd     string
	args    []string
	managed bool
}

const resolvedSessionMarker = "__resolved_session__"

type cliRuntimeHarness struct {
	stdout            bytes.Buffer
	stderr            bytes.Buffer
	usageCalls        int
	shouldTakeover    bool
	tryTakeoverResult bool
	calls             []cliCall
}

func newCLIRuntimeHarness() *cliRuntimeHarness {
	return &cliRuntimeHarness{}
}

func resolveTestSessions(calls []cliCall) []cliCall {
	resolved := ResolveSessionName("", false)
	out := make([]cliCall, len(calls))
	for i, call := range calls {
		out[i] = call
		if out[i].session == resolvedSessionMarker {
			out[i].session = resolved
		}
	}
	return out
}

func (h *cliRuntimeHarness) runtime() Runtime {
	return Runtime{
		Stdout: &h.stdout,
		Stderr: &h.stderr,
		AttachSession: func(sessionName string) error {
			h.calls = append(h.calls, cliCall{kind: "attach", session: sessionName})
			return nil
		},
		WriteVersionOutput: func(w io.Writer, args []string) error {
			h.calls = append(h.calls, cliCall{kind: "version", args: append([]string(nil), args...)})
			_, err := io.WriteString(w, "version\n")
			return err
		},
		InstallTerminfo: func() error {
			h.calls = append(h.calls, cliCall{kind: "install-terminfo"})
			return nil
		},
		RunDebugCommand: func(sessionName string, args []string) {
			h.calls = append(h.calls, cliCall{
				kind:    "debug",
				session: sessionName,
				args:    append([]string(nil), args...),
			})
		},
		RunServer: func(sessionName string, managed bool) {
			h.calls = append(h.calls, cliCall{kind: "run-server", session: sessionName, managed: managed})
		},
		RunServerCommand: func(sessionName, cmdName string, args []string) {
			h.calls = append(h.calls, cliCall{
				kind:    "server-command",
				session: sessionName,
				cmd:     cmdName,
				args:    append([]string(nil), args...),
			})
		},
		RunEventsCommand: func(sessionName string, args []string) {
			h.calls = append(h.calls, cliCall{
				kind:    "events",
				session: sessionName,
				args:    append([]string(nil), args...),
			})
		},
		CheckNesting: func(sessionName string) {
			h.calls = append(h.calls, cliCall{kind: "check-nesting", session: sessionName})
		},
		ShouldTakeover: func() bool {
			return h.shouldTakeover
		},
		TryTakeover: func(sessionName string) bool {
			h.calls = append(h.calls, cliCall{kind: "try-takeover", session: sessionName})
			return h.tryTakeoverResult
		},
		PrintUsage: func() {
			h.usageCalls++
		},
	}
}
