package test

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func captureHasActiveZoomedPane(capture proto.CaptureJSON, name string) bool {
	for _, pane := range capture.Panes {
		if pane.Name == name {
			return pane.Active && pane.Zoomed
		}
	}
	return false
}

func TestCaptureHasActiveZoomedPane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		capture proto.CaptureJSON
		pane    string
		want    bool
	}{
		{
			name: "matching active zoomed pane",
			capture: proto.CaptureJSON{
				Panes: []proto.CapturePane{{
					Name:   "pane-1",
					Active: true,
					Zoomed: true,
				}},
			},
			pane: "pane-1",
			want: true,
		},
		{
			name: "matching pane without zoom",
			capture: proto.CaptureJSON{
				Panes: []proto.CapturePane{{
					Name:   "pane-1",
					Active: true,
				}},
			},
			pane: "pane-1",
			want: false,
		},
		{
			name: "matching pane without focus",
			capture: proto.CaptureJSON{
				Panes: []proto.CapturePane{{
					Name:   "pane-1",
					Zoomed: true,
				}},
			},
			pane: "pane-1",
			want: false,
		},
		{
			name: "different pane is active zoomed",
			capture: proto.CaptureJSON{
				Panes: []proto.CapturePane{
					{Name: "pane-1"},
					{Name: "pane-2", Active: true, Zoomed: true},
				},
			},
			pane: "pane-1",
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := captureHasActiveZoomedPane(tt.capture, tt.pane); got != tt.want {
				t.Fatalf("captureHasActiveZoomedPane(%q) = %v, want %v", tt.pane, got, tt.want)
			}
		})
	}
}
