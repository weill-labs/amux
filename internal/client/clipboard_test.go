package client

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
	t.Setenv("TMUX", "")

	var wrote bytes.Buffer
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			t.Fatalf("runClipboardCommand(%v, %q) should not run under SSH", cmd, text)
			return nil
		},
	}

	copyToClipboardLocal(deps, "remote copy")

	if got, want := wrote.String(), osc52ClipboardSequence("remote copy"); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}

func TestCopyToClipboardWrapsOSC52ForTmuxWhenSSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "1")
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")

	var wrote bytes.Buffer
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			t.Fatalf("runClipboardCommand(%v, %q) should not run under SSH", cmd, text)
			return nil
		},
	}

	copyToClipboardLocal(deps, "remote copy")

	if got, want := wrote.String(), ansi.TmuxPassthrough(osc52ClipboardSequence("remote copy")); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}

func TestCopyToClipboardFallsBackToOSC52WhenSystemClipboardFails(t *testing.T) {
	t.Setenv("TMUX", "")

	var wrote bytes.Buffer
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			return errors.New("clipboard unavailable")
		},
	}

	copyToClipboardLocal(deps, "fallback copy")

	if got, want := wrote.String(), osc52ClipboardSequence("fallback copy"); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}

func TestCopyToClipboardWrapsFallbackOSC52ForTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")

	var wrote bytes.Buffer
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			return errors.New("clipboard unavailable")
		},
	}

	copyToClipboardLocal(deps, "fallback copy")

	if got, want := wrote.String(), ansi.TmuxPassthrough(osc52ClipboardSequence("fallback copy")); got != want {
		t.Fatalf("clipboard output = %q, want %q", got, want)
	}
}

func TestCopyToClipboardUsesSystemClipboardOutsideSSH(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")

	var wrote bytes.Buffer
	var calls [][]string
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			calls = append(calls, append([]string(nil), cmd...))
			if text != "local copy" {
				t.Fatalf("clipboard text = %q, want %q", text, "local copy")
			}
			return nil
		},
	}

	copyToClipboardLocal(deps, "local copy")

	if wrote.Len() != 0 {
		t.Fatalf("clipboard stdout = %q, want empty", wrote.String())
	}
	if want := [][]string{{"pbcopy"}}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("clipboard commands = %v, want %v", calls, want)
	}
}

func TestCopyToClipboardEmptyTextDoesNothing(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "1")

	var wrote bytes.Buffer
	deps := clipboardDeps{
		stdout: &wrote,
		runCmd: func(cmd []string, text string) error {
			t.Fatalf("runClipboardCommand(%v, %q) should not run for empty text", cmd, text)
			return nil
		},
	}

	copyToClipboardLocal(deps, "")

	if wrote.Len() != 0 {
		t.Fatalf("clipboard stdout = %q, want empty", wrote.String())
	}
}

func TestTrySystemClipboardFallsThroughCommands(t *testing.T) {
	t.Parallel()

	var calls [][]string
	runCmd := func(cmd []string, text string) error {
		calls = append(calls, append([]string(nil), cmd...))
		if text != "fallback order" {
			t.Fatalf("clipboard text = %q, want %q", text, "fallback order")
		}
		if len(calls) < 3 {
			return errors.New("missing binary")
		}
		return nil
	}

	if !trySystemClipboard(runCmd, "fallback order") {
		t.Fatal("trySystemClipboard() = false, want true")
	}

	want := [][]string{
		{"pbcopy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("clipboard commands = %v, want %v", calls, want)
	}
}

func TestWriteOSC52ClipboardNilWriter(t *testing.T) {
	writeOSC52Clipboard(nil, "ignored")
}

func TestOSC52ClipboardOutputWrapsForTmux(t *testing.T) {
	t.Parallel()

	lookup := func(key string) (string, bool) {
		if key == "TMUX" {
			return "/tmp/tmux-501/default,1,0", true
		}
		return "", false
	}

	if got, want := osc52ClipboardOutput(lookup, "hello"), ansi.TmuxPassthrough(osc52ClipboardSequence("hello")); got != want {
		t.Fatalf("osc52ClipboardOutput() = %q, want %q", got, want)
	}
}
