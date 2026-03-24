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
		{name: "default horizontal", args: nil, want: nil},
		{name: "vertical flag", args: []string{"--vertical"}, want: []string{"v"}},
		{name: "horizontal flag", args: []string{"--horizontal"}, want: nil},
		{name: "root vertical flag", args: []string{"root", "--vertical"}, want: []string{"root", "v"}},
		{name: "host and vertical flag", args: []string{"--host", "gpu-server", "--vertical"}, want: []string{"v", "--host", "gpu-server"}},
		{name: "background flag", args: []string{"--background"}, want: []string{"--background"}},
		{name: "name and background", args: []string{"--name", "bg-pane", "--background"}, want: []string{"--name", "bg-pane", "--background"}},
		{name: "legacy vertical shorthand", args: []string{"v"}, want: []string{"v"}},
		{name: "mixed legacy and flag", args: []string{"v", "--vertical"}, want: []string{"v"}},
		{name: "conflicting directions", args: []string{"--vertical", "--horizontal"}, wantErr: "conflicting split directions"},
		{name: "missing host value", args: []string{"--host"}, wantErr: "--host requires a value"},
		{name: "unknown arg", args: []string{"banana"}, wantErr: "unknown split arg"},
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
		{name: "default when unset", want: "default"},
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
