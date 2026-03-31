package config

import (
	"slices"
	"testing"
)

func TestDefaultKeybindings(t *testing.T) {
	t.Parallel()

	kb := DefaultKeybindings()
	if kb.Prefix != 0x01 {
		t.Errorf("default prefix = %d, want 0x01 (Ctrl-a)", kb.Prefix)
	}

	tests := []struct {
		name string
		key  byte
		want Binding
	}{
		{
			name: "backslash creates a root vertical split",
			key:  '\\',
			want: Binding{Action: "split", Args: []string{"root", "v", "--focus"}},
		},
		{
			name: "pipe creates a local vertical split",
			key:  '|',
			want: Binding{Action: "split", Args: []string{"v", "--focus"}},
		},
		{
			name: "d detaches",
			key:  'd',
			want: Binding{Action: "detach"},
		},
		{
			name: "o focuses the next pane",
			key:  'o',
			want: Binding{Action: "focus", Args: []string{"next"}},
		},
		{
			name: "q displays pane numbers",
			key:  'q',
			want: Binding{Action: "display-panes"},
		},
		{
			name: "a adds a pane",
			key:  'a',
			want: Binding{Action: "add-pane"},
		},
		{
			name: "s opens choose tree",
			key:  's',
			want: Binding{Action: "choose-tree"},
		},
		{
			name: "w opens choose window",
			key:  'w',
			want: Binding{Action: "choose-window"},
		},
		{
			name: "comma opens rename window prompt",
			key:  ',',
			want: Binding{Action: "rename-window"},
		},
		{
			name: "m remains reserved for compat bell",
			key:  'm',
			want: Binding{Action: "compat-bell"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := kb.Bindings[tt.key]
			if !ok {
				t.Fatalf("default: %q should be bound", tt.key)
			}
			if got.Action != tt.want.Action {
				t.Fatalf("default: %q action = %q, want %q", tt.key, got.Action, tt.want.Action)
			}
			if !slices.Equal(got.Args, tt.want.Args) {
				t.Fatalf("default: %q args = %v, want %v", tt.key, got.Args, tt.want.Args)
			}
		})
	}

	if _, ok := kb.Bindings['M']; ok {
		t.Error("default: M should be unbound")
	}
}
