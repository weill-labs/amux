package test

import "testing"

func TestShellSingleQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain",
			in:   "shared",
			want: "'shared'",
		},
		{
			name: "embedded single quote",
			in:   "echo 'two words'",
			want: `'echo '"'"'two words'"'"''`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shellSingleQuote(tt.in); got != tt.want {
				t.Fatalf("shellSingleQuote(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNestedAmuxCommand(t *testing.T) {
	t.Parallel()

	got := nestedAmuxCommand("/tmp/amux", "session 1", "wait", "content", "shared", "WINDOW TWO", "--timeout", "5s")
	want := "'/tmp/amux' -s 'session 1' 'wait' 'content' 'shared' 'WINDOW TWO' '--timeout' '5s'"
	if got != want {
		t.Fatalf("nestedAmuxCommand(...) = %q, want %q", got, want)
	}
}
