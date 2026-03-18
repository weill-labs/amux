package remote

import "testing"

func TestNormalizeAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "bare hostname", addr: "myhost", want: "myhost:22"},
		{name: "bare IP", addr: "10.0.0.1", want: "10.0.0.1:22"},
		{name: "with port", addr: "10.0.0.1:2222", want: "10.0.0.1:2222"},
		{name: "with default port", addr: "myhost:22", want: "myhost:22"},
		{name: "IPv6 bare", addr: "::1", want: "::1:22"},
		{name: "IPv6 bracketed with port", addr: "[::1]:22", want: "[::1]:22"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeAddr(tt.addr); got != tt.want {
				t.Errorf("normalizeAddr(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
