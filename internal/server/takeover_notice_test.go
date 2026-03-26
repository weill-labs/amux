package server

import (
	"errors"
	"testing"
)

func TestSummarizeTakeoverAttachError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "host key verification keeps first meaningful line",
			err: errors.New(`SSH dial 100.118.104.90:22: ssh: handshake failed: amux: SSH host key verification failed for 100.118.104.90
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!    @`),
			want: "SSH host key verification failed for 100.118.104.90",
		},
		{
			name: "plain wrapped error trims amux prefix",
			err:  errors.New("amux: waiting for remote socket /tmp/amux-1000/main@host: timeout"),
			want: "waiting for remote socket /tmp/amux-1000/main@host: timeout",
		},
		{
			name: "nil error falls back",
			err:  nil,
			want: "takeover failed",
		},
		{
			name: "blank error falls back",
			err:  errors.New(" \n "),
			want: "takeover failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := summarizeTakeoverAttachError(tt.err); got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTakeoverFailureNotice(t *testing.T) {
	t.Parallel()

	err := errors.New(`SSH dial 10.0.0.5:22: ssh: handshake failed: amux: SSH host key verification failed for 10.0.0.5
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@`)
	got := formatTakeoverFailureNotice("gpu-box", "10.0.0.5:22", err)
	want := "takeover gpu-box (10.0.0.5:22): SSH host key verification failed for 10.0.0.5"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

func TestFormatTakeoverFailureNoticeDefaultsToRemoteTarget(t *testing.T) {
	t.Parallel()

	got := formatTakeoverFailureNotice("", "", errors.New("amux: timeout"))
	want := "takeover remote: timeout"
	if got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}
