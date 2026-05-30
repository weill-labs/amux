package cli

import (
	"reflect"
	"testing"
)

func TestMCPServerCLICommandHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
		wantCalls  []cliCall
	}{
		{
			name:       "help",
			args:       []string{"mcp-server", "--help"},
			wantStdout: mcpServerUsage + "\n",
		},
		{
			name:       "rejects args",
			args:       []string{"mcp-server", "extra"},
			wantExit:   1,
			wantStderr: mcpServerUsage + "\n",
		},
		{
			name:     "dispatch",
			args:     []string{"mcp-server"},
			wantExit: 0,
			wantCalls: []cliCall{{
				kind:    "mcp-server",
				session: resolvedSessionMarker,
			}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newCLIRuntimeHarness()
			gotExit := RunWithRuntime(tt.args, h.runtime())
			if gotExit != tt.wantExit {
				t.Fatalf("RunWithRuntime(%v) exit = %d, want %d", tt.args, gotExit, tt.wantExit)
			}
			if got := h.stdout.String(); got != tt.wantStdout {
				t.Fatalf("stdout = %q, want %q", got, tt.wantStdout)
			}
			if got := h.stderr.String(); got != tt.wantStderr {
				t.Fatalf("stderr = %q, want %q", got, tt.wantStderr)
			}
			if got, want := h.calls, resolveTestSessions(tt.wantCalls); !reflect.DeepEqual(got, want) {
				t.Fatalf("calls = %#v, want %#v", got, want)
			}
		})
	}
}
