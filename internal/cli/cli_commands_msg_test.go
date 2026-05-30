package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("boom")
}

func TestPrepareMsgCLIArgs(t *testing.T) {
	t.Parallel()

	bodyPath := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(bodyPath, []byte("file body\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(body): %v", err)
	}

	tests := []struct {
		name    string
		stdin   io.Reader
		args    []string
		want    []string
		wantErr string
	}{
		{
			name: "non send command is unchanged",
			args: []string{"inbox", "pane-1"},
			want: []string{"inbox", "pane-1"},
		},
		{
			name: "body flag is forwarded",
			args: []string{"send", "--to", "pane-1", "--body", "hello"},
			want: []string{"send", "--to", "pane-1", "--body", "hello"},
		},
		{
			name: "body file becomes body flag",
			args: []string{"send", "--to", "pane-1", "--body-file", bodyPath},
			want: []string{"send", "--to", "pane-1", "--body", "file body\n"},
		},
		{
			name:  "stdin becomes body flag",
			stdin: strings.NewReader("stdin body"),
			args:  []string{"send", "--to", "pane-1"},
			want:  []string{"send", "--to", "pane-1", "--body", "stdin body"},
		},
		{
			name:    "missing body value",
			args:    []string{"send", "--body"},
			wantErr: "missing value for --body",
		},
		{
			name:    "missing body file value",
			args:    []string{"send", "--body-file"},
			wantErr: "missing value for --body-file",
		},
		{
			name:    "body and body file conflict",
			args:    []string{"send", "--body", "hello", "--body-file", bodyPath},
			wantErr: "mutually exclusive",
		},
		{
			name:    "body and stdin conflict",
			stdin:   strings.NewReader("stdin body"),
			args:    []string{"send", "--body", "hello"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "body file and stdin conflict",
			stdin:   strings.NewReader("stdin body"),
			args:    []string{"send", "--body-file", bodyPath},
			wantErr: "mutually exclusive",
		},
		{
			name:    "body file read error",
			args:    []string{"send", "--body-file", filepath.Join(t.TempDir(), "missing.txt")},
			wantErr: "reading --body-file",
		},
		{
			name:    "stdin read error",
			stdin:   errReader{},
			args:    []string{"send"},
			wantErr: "reading stdin",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := prepareMsgCLIArgs(tt.stdin, tt.args)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("prepareMsgCLIArgs() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepareMsgCLIArgs(): %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("prepareMsgCLIArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestShouldReadMsgStdin(t *testing.T) {
	t.Parallel()

	if shouldReadMsgStdin(nil) {
		t.Fatal("nil stdin should not be read")
	}
	if !shouldReadMsgStdin(strings.NewReader("body")) {
		t.Fatal("non-file stdin should be read")
	}

	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if !shouldReadMsgStdin(f) {
		t.Fatal("regular file stdin should be read")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if shouldReadMsgStdin(f) {
		t.Fatal("stdin with stat error should not be read")
	}
}

func TestMsgCLICommandHandler(t *testing.T) {
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
			args:       []string{"msg", "--help"},
			wantStdout: msgUsage + "\n",
		},
		{
			name:       "subcommand help",
			args:       []string{"msg", "send", "--help"},
			wantStdout: msgUsage + "\n",
		},
		{
			name:       "missing subcommand",
			args:       []string{"msg"},
			wantExit:   1,
			wantStderr: msgUsage + "\n",
		},
		{
			name:       "prepare error",
			args:       []string{"msg", "send", "--body"},
			wantExit:   1,
			wantStderr: "amux msg: missing value for --body\n",
		},
		{
			name:     "dispatch",
			args:     []string{"msg", "send", "--to", "pane-1", "--body", "hello"},
			wantExit: 0,
			wantCalls: []cliCall{{
				kind:    "server-command",
				session: resolvedSessionMarker,
				cmd:     "msg",
				args:    []string{"send", "--to", "pane-1", "--body", "hello"},
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
