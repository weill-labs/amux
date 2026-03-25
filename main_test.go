package main

import (
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
