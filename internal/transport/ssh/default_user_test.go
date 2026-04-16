package ssh

import (
	"errors"
	"os/user"
	"testing"
)

func TestDefaultSSHUserResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		envUser     string
		currentUser func() (*user.User, error)
		want        string
	}{
		{
			name:    "uses current user when lookup succeeds",
			envUser: "",
			currentUser: func() (*user.User, error) {
				return &user.User{Username: "alice"}, nil
			},
			want: "alice",
		},
		{
			name:    "falls back to USER when lookup fails",
			envUser: "builder",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "builder",
		},
		{
			name:    "returns empty when lookup fails and USER is unset",
			envUser: "",
			currentUser: func() (*user.User, error) {
				return nil, errors.New("lookup failed")
			},
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := defaultSSHUser(tt.currentUser, func(key string) string {
				if key != "USER" {
					t.Fatalf("lookup env key = %q, want USER", key)
				}
				return tt.envUser
			}, func(error) {})
			if got != tt.want {
				t.Fatalf("defaultSSHUser() = %q, want %q", got, tt.want)
			}
		})
	}
}
