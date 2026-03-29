package main

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseSplitArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}{
		{name: "pane only", args: []string{"pane-1"}, want: []string{"pane-1"}},
		{name: "pane horizontal", args: []string{"pane-1", "--horizontal"}, want: []string{"pane-1"}},
		{name: "pane vertical", args: []string{"pane-1", "--vertical"}, want: []string{"pane-1", "v"}},
		{name: "pane root vertical", args: []string{"pane-1", "root", "--vertical"}, want: []string{"pane-1", "root", "v"}},
		{name: "pane host vertical", args: []string{"pane-1", "--host", "gpu-server", "--vertical"}, want: []string{"pane-1", "v", "--host", "gpu-server"}},
		{name: "pane named", args: []string{"pane-1", "--name", "worker"}, want: []string{"pane-1", "--name", "worker"}},
		{name: "pane legacy vertical", args: []string{"pane-1", "v"}, want: []string{"pane-1", "v"}},
		{name: "numeric pane id", args: []string{"42"}, want: []string{"42"}},
		{name: "no args", args: nil, wantErr: "pane argument required"},
		{name: "flags only", args: []string{"--vertical"}, wantErr: "pane argument required"},
		{name: "conflicting directions", args: []string{"pane-1", "--vertical", "--horizontal"}, wantErr: "conflicting split directions"},
		{name: "legacy background rejected", args: []string{"pane-1", "--background"}, wantErr: `unknown split arg "--background"`},
		{name: "legacy pane flag rejected", args: []string{"--pane", "pane-1"}, wantErr: `unknown split arg "--pane"`},
		{name: "missing host value", args: []string{"pane-1", "--host"}, wantErr: "--host requires a value"},
		{name: "two pane refs", args: []string{"pane-1", "pane-2"}, wantErr: "unknown split arg"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSplitArgs(tt.args)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseSplitArgs(%v): expected error containing %q", tt.args, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseSplitArgs(%v): error = %q, want substring %q", tt.args, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSplitArgs(%v): unexpected error: %v", tt.args, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseSplitArgs(%v) = %v, want %v", tt.args, got, tt.want)
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
			name:         "type-keys short help",
			args:         []string{"-h"},
			usage:        typeKeysUsage,
			minArgs:      1,
			wantHandled:  true,
			wantExitCode: 0,
			wantStdout:   typeKeysUsage + "\n",
		},
		{
			name:         "type-keys dispatch with keys",
			args:         []string{"abc"},
			usage:        typeKeysUsage,
			minArgs:      1,
			wantHandled:  false,
			wantExitCode: 0,
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
