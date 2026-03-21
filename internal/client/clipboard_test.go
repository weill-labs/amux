package client

import (
	"bytes"
	"errors"
	"testing"
)

func TestPreferOSC52Clipboard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "no ssh env",
			want: false,
		},
		{
			name: "ssh connection",
			env:  map[string]string{"SSH_CONNECTION": "1"},
			want: true,
		},
		{
			name: "ssh client",
			env:  map[string]string{"SSH_CLIENT": "1"},
			want: true,
		},
		{
			name: "ssh tty",
			env:  map[string]string{"SSH_TTY": "/dev/pts/1"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lookup := func(key string) (string, bool) {
				value, ok := tt.env[key]
				return value, ok
			}
			if got := preferOSC52Clipboard(lookup); got != tt.want {
				t.Fatalf("preferOSC52Clipboard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOSC52ClipboardSequence(t *testing.T) {
	t.Parallel()

	if got, want := osc52ClipboardSequence("hello\n"), "\x1b]52;c;aGVsbG8K\a"; got != want {
		t.Fatalf("osc52ClipboardSequence() = %q, want %q", got, want)
	}
}

func TestCopyToClipboardPrefersOSC52OverSystemClipboardWhenSSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "1")

	prevStdout := clipboardStdout
	prevRun := runClipboardCommand
	var wrote bytes.Buffer
	clipboardStdout = &wrote
	runClipboardCommand = func(cmd []string, text string) error {
		t.Fatalf("runClipboardCommand(%v, %q) should not run under SSH", cmd, text)
		return nil
	}
	t.Cleanup(func() {
		clipboardStdout = prevStdout
		runClipboardCommand = prevRun
	})

	CopyToClipboard("remote copy")

	if got, want := wrote.String(), osc52ClipboardSequence("remote copy"); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}

func TestCopyToClipboardFallsBackToOSC52WhenSystemClipboardFails(t *testing.T) {
	prevStdout := clipboardStdout
	prevRun := runClipboardCommand
	var wrote bytes.Buffer
	clipboardStdout = &wrote
	runClipboardCommand = func(cmd []string, text string) error {
		return errors.New("clipboard unavailable")
	}
	t.Cleanup(func() {
		clipboardStdout = prevStdout
		runClipboardCommand = prevRun
	})

	CopyToClipboard("fallback copy")

	if got, want := wrote.String(), osc52ClipboardSequence("fallback copy"); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}
