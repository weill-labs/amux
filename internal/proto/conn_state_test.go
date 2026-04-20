package proto

import "testing"

func TestConnStateConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  ConnState
		want string
	}{
		{name: "disconnected", got: Disconnected, want: "disconnected"},
		{name: "connecting", got: Connecting, want: "connecting"},
		{name: "connected", got: Connected, want: "connected"},
		{name: "reconnecting", got: Reconnecting, want: "reconnecting"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := string(tt.got); got != tt.want {
				t.Fatalf("string(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
