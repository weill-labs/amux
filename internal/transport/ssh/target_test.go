package ssh

import (
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	t.Parallel()

	defaultUser := DefaultSSHUser()
	tests := []struct {
		name        string
		raw         string
		defaultUser string
		want        SSHTarget
		wantErr     string
	}{
		{
			name:        "explicit user and default session",
			raw:         "alice@example.com",
			defaultUser: "ubuntu",
			want: SSHTarget{
				User:    "alice",
				Host:    "example.com",
				Port:    "22",
				Session: "main",
			},
		},
		{
			name:        "default user with explicit session",
			raw:         "builder:work",
			defaultUser: "deploy",
			want: SSHTarget{
				User:    "deploy",
				Host:    "builder",
				Port:    "22",
				Session: "work",
			},
		},
		{
			name:        "explicit user and session",
			raw:         "alice@example.com:logs",
			defaultUser: "ubuntu",
			want: SSHTarget{
				User:    "alice",
				Host:    "example.com",
				Port:    "22",
				Session: "logs",
			},
		},
		{
			name:        "bracketed ipv6 host",
			raw:         "alice@[::1]:dev",
			defaultUser: "ubuntu",
			want: SSHTarget{
				User:    "alice",
				Host:    "::1",
				Port:    "22",
				Session: "dev",
			},
		},
		{
			name:        "bracketed ipv6 default session",
			raw:         "[::1]",
			defaultUser: "ubuntu",
			want: SSHTarget{
				User:    "ubuntu",
				Host:    "::1",
				Port:    "22",
				Session: "main",
			},
		},
		{
			name:        "blank default uses resolved current user",
			raw:         "builder",
			defaultUser: "",
			want: SSHTarget{
				User:    defaultUser,
				Host:    "builder",
				Port:    "22",
				Session: "main",
			},
		},
		{
			name:        "empty target",
			raw:         "",
			defaultUser: "ubuntu",
			wantErr:     "ssh target",
		},
		{
			name:        "missing user before at-sign",
			raw:         "@example.com",
			defaultUser: "ubuntu",
			wantErr:     "user",
		},
		{
			name:        "missing host after user",
			raw:         "alice@",
			defaultUser: "ubuntu",
			wantErr:     "host",
		},
		{
			name:        "extra session separator",
			raw:         "example.com:one:two",
			defaultUser: "ubuntu",
			wantErr:     "invalid",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTarget(tt.raw, tt.defaultUser)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseTarget(%q, %q) error = nil, want substring %q", tt.raw, tt.defaultUser, tt.wantErr)
				}
				if got != (SSHTarget{}) {
					t.Fatalf("ParseTarget(%q, %q) target = %#v, want zero value on error", tt.raw, tt.defaultUser, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseTarget(%q, %q) error = %q, want substring %q", tt.raw, tt.defaultUser, err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTarget(%q, %q) error = %v", tt.raw, tt.defaultUser, err)
			}
			if got != tt.want {
				t.Fatalf("ParseTarget(%q, %q) = %#v, want %#v", tt.raw, tt.defaultUser, got, tt.want)
			}
		})
	}
}
