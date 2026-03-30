package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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
		{name: "focused spawn", args: []string{"--focus"}, wantCmd: "spawn-focus", wantArgs: []string{}},
		{name: "split at pane", args: []string{"--at", "pane-1"}, wantCmd: "split", wantArgs: []string{"pane-1"}},
		{name: "split active vertical", args: []string{"--vertical"}, wantCmd: "split", wantArgs: []string{"v"}},
		{name: "split root vertical", args: []string{"--at", "pane-1", "--root", "--vertical"}, wantCmd: "split", wantArgs: []string{"pane-1", "root", "v"}},
		{name: "split with metadata", args: []string{"--at", "pane-1", "--task", "build", "--color", "blue"}, wantCmd: "split", wantArgs: []string{"pane-1", "--task", "build", "--color", "blue"}},
		{name: "spiral add", args: []string{"--spiral", "--name", "worker"}, wantCmd: "add-pane", wantArgs: []string{"--name", "worker"}},
		{name: "spiral add focus", args: []string{"--spiral", "--focus"}, wantCmd: "add-pane-focus", wantArgs: []string{}},
		{name: "conflicting directions", args: []string{"--vertical", "--horizontal"}, wantErrText: spawnUsage},
		{name: "spiral with split flags rejected", args: []string{"--spiral", "--at", "pane-1"}, wantErrText: spawnUsage},
		{name: "missing at value", args: []string{"--at"}, wantErrText: spawnUsage},
		{name: "unknown arg", args: []string{"pane-1"}, wantErrText: spawnUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCmd, gotArgs, err := parseSpawnCommandArgs(tt.args)
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("parseSpawnCommandArgs(%v): expected error containing %q", tt.args, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("parseSpawnCommandArgs(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSpawnCommandArgs(%v): unexpected error: %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("parseSpawnCommandArgs(%v) cmd = %q, want %q", tt.args, gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("parseSpawnCommandArgs(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
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
		{name: "default when unset", want: defaultSessionName},
		{name: "use AMUX_SESSION when flag omitted", envSession: "current-session", want: "current-session"},
		{name: "explicit session without env", explicit: "other-session", explicitSet: true, want: "other-session"},
		{name: "explicit session beats AMUX_SESSION", explicit: "other-session", explicitSet: true, envSession: "current-session", want: "other-session"},
		{name: "explicit empty session still beats AMUX_SESSION", explicit: "", explicitSet: true, envSession: "current-session", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AMUX_SESSION", tt.envSession)

			if got := resolveSessionName(tt.explicit, tt.explicitSet); got != tt.want {
				t.Fatalf("resolveSessionName(%q, %t) = %q, want %q", tt.explicit, tt.explicitSet, got, tt.want)
			}
		})
	}
}

func TestDefaultSessionNameValue(t *testing.T) {
	t.Parallel()

	if defaultSessionName != "main" {
		t.Fatalf("defaultSessionName = %q, want %q", defaultSessionName, "main")
	}
}

func TestBuildVersionIncludesCheckpointVersion(t *testing.T) {
	t.Parallel()

	got := buildVersion()
	want := "checkpoint v" + strconv.Itoa(checkpoint.ServerCheckpointVersion)
	if !strings.Contains(got, want) {
		t.Fatalf("buildVersion() = %q, want substring %q", got, want)
	}
}

func TestWriteVersionOutputJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersionOutput(&out, []string{"--json"}); err != nil {
		t.Fatalf("writeVersionOutput(--json): %v", err)
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
	if err := writeVersionOutput(&out, []string{"--hash"}); err != nil {
		t.Fatalf("writeVersionOutput(--hash): %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != buildHash() {
		t.Fatalf("hash output = %q, want %q", got, buildHash())
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
		{name: "default when unset", args: []string{"status"}, wantName: defaultSessionName, wantArgs: []string{"status"}},
		{name: "uses AMUX_SESSION when flag omitted", args: []string{"status"}, envSession: "current-session", wantName: "current-session", wantArgs: []string{"status"}},
		{name: "strips explicit session flag", args: []string{"-s", "other-session", "status"}, envSession: "current-session", wantName: "other-session", wantArgs: []string{"status"}},
		{name: "strips explicit session flag from middle", args: []string{"events", "-s", "other-session", "--no-reconnect"}, wantName: "other-session", wantArgs: []string{"events", "--no-reconnect"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AMUX_SESSION", tt.envSession)

			gotName, gotArgs := resolveInvocationSession(tt.args)
			if gotName != tt.wantName {
				t.Fatalf("resolveInvocationSession(%v) session = %q, want %q", tt.args, gotName, tt.wantName)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("resolveInvocationSession(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
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

			gotCmd, gotArgs, gotHandled, err := resolveCanonicalSessionCommand(tt.args)
			if gotHandled != tt.wantHandled {
				t.Fatalf("resolveCanonicalSessionCommand(%v) handled = %v, want %v", tt.args, gotHandled, tt.wantHandled)
			}
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("resolveCanonicalSessionCommand(%v): expected error containing %q", tt.args, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("resolveCanonicalSessionCommand(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCanonicalSessionCommand(%v): unexpected error: %v", tt.args, err)
			}
			if gotCmd != tt.wantCmd {
				t.Fatalf("resolveCanonicalSessionCommand(%v) cmd = %q, want %q", tt.args, gotCmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("resolveCanonicalSessionCommand(%v) args = %v, want %v", tt.args, gotArgs, tt.wantArgs)
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

			handled, exitCode := maybePrintKeyCommandUsage(&stdout, &stderr, tt.args, tt.usage, tt.minArgs)
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
			gotCmd, gotArgs, err := parseSwapArgs(tt.args)
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
			gotCmd, gotArgs, err := parseMoveArgs(tt.args)
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
			gotCmd, gotArgs, err := parseLeadArgs(tt.args)
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
			err := validateMetaArgs(tt.args)
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
			name:        "wait long help reflects idle rename",
			args:        []string{"wait", "--help"},
			wantHandled: true,
			wantStdout:  "usage: amux wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ...\n",
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

			handled := maybePrintCommandHelp(&stdout, tt.args)
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
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	printUsage()
	if err := w.Close(); err != nil {
		t.Fatalf("Close write pipe: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close read pipe: %v", err)
	}

	if strings.Contains(buf.String(), "amux [-s session] delegate <pane>") {
		t.Fatalf("printUsage should omit delegate:\n%s", buf.String())
	}
}
