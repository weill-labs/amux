package meta

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

type stubMetaContext struct{}

func (stubMetaContext) ResolvePaneForMutation(string) (*mux.Pane, error) { return nil, nil }

func (stubMetaContext) QueryPaneKV(string, []string) (string, error) { return "", nil }

func TestMetaUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing subcommand"},
		{name: "unknown subcommand", args: []string{"wat"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Meta(stubMetaContext{}, tt.args)
			if got.Err == nil || got.Err.Error() != MetaUsage {
				t.Fatalf("Meta(%v) error = %v, want %q", tt.args, got.Err, MetaUsage)
			}
		})
	}
}
