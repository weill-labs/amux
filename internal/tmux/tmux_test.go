package tmux

import (
	"testing"
)

func TestPaneFieldsIsAmux(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fields PaneFields
		want   bool
	}{
		{"with name", PaneFields{Name: "my-agent"}, true},
		{"empty name", PaneFields{}, false},
		{"only host", PaneFields{Host: "remote"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.fields.IsAmux(); got != tt.want {
				t.Errorf("IsAmux() = %v, want %v", got, tt.want)
			}
		})
	}
}
