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
		{name: "pane only", args: []string{"pane-1"}, want: []string{"--pane", "pane-1"}},
		{name: "pane horizontal", args: []string{"pane-1", "--horizontal"}, want: []string{"--pane", "pane-1"}},
		{name: "pane vertical", args: []string{"pane-1", "--vertical"}, want: []string{"--pane", "pane-1", "v"}},
		{name: "pane root vertical", args: []string{"pane-1", "root", "--vertical"}, want: []string{"--pane", "pane-1", "root", "v"}},
		{name: "pane host vertical", args: []string{"pane-1", "--host", "gpu-server", "--vertical"}, want: []string{"--pane", "pane-1", "v", "--host", "gpu-server"}},
		{name: "pane background", args: []string{"pane-1", "--background"}, want: []string{"--pane", "pane-1", "--background"}},
		{name: "pane name background", args: []string{"pane-1", "--name", "bg-pane", "--background"}, want: []string{"--pane", "pane-1", "--name", "bg-pane", "--background"}},
		{name: "pane legacy vertical", args: []string{"pane-1", "v"}, want: []string{"--pane", "pane-1", "v"}},
		{name: "numeric pane id", args: []string{"42"}, want: []string{"--pane", "42"}},
		{name: "no args", args: nil, wantErr: "pane argument required"},
		{name: "flags only", args: []string{"--vertical"}, wantErr: "pane argument required"},
		{name: "conflicting directions", args: []string{"pane-1", "--vertical", "--horizontal"}, wantErr: "conflicting split directions"},
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
