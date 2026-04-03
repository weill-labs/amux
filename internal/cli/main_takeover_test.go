package cli

import "testing"

func TestShouldAttemptTakeover(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"ssh from amux pane", map[string]string{"SSH_CONNECTION": "1.2.3.4 1234 5.6.7.8 22", "TERM": "amux", "AMUX_PANE": ""}, true},
		{"ssh from phone", map[string]string{"SSH_CONNECTION": "1.2.3.4 1234 5.6.7.8 22", "TERM": "xterm-256color", "AMUX_PANE": ""}, false},
		{"no ssh", map[string]string{"SSH_CONNECTION": "", "TERM": "amux", "AMUX_PANE": ""}, false},
		{"already in remote amux pane", map[string]string{"SSH_CONNECTION": "1.2.3.4 1234 5.6.7.8 22", "TERM": "amux", "AMUX_PANE": "1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := ShouldAttemptTakeover(); got != tt.want {
				t.Errorf("ShouldAttemptTakeover() = %v, want %v", got, tt.want)
			}
		})
	}
}
