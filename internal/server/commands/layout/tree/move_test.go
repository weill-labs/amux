package tree

import "testing"

func TestParseMoveArgsUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing target", args: []string{"pane-1", "--before"}, want: MoveUsage},
		{name: "invalid flag", args: []string{"pane-1", "--around", "pane-2"}, want: MoveUsage},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, _, err := ParseMoveArgs(tt.args)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("ParseMoveArgs(%v) error = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}

func TestParseMoveToArgsUsage(t *testing.T) {
	t.Parallel()

	_, _, err := ParseMoveToArgs([]string{"pane-1"})
	if err == nil || err.Error() != MoveToUsage {
		t.Fatalf("ParseMoveToArgs() error = %v, want %q", err, MoveToUsage)
	}
}
