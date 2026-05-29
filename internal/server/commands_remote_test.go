package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

func TestParseRemoteAddArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantName    string
		wantHost    config.Host
		wantErrText string
	}{
		{
			name:     "valid default session",
			args:     []string{"hetzner-1", "--ssh", "host", "--socket", "/tmp/amux/main"},
			wantName: "hetzner-1",
			wantHost: config.Host{SSH: "host", SocketPath: "/tmp/amux/main", Session: DefaultSessionName},
		},
		{
			name:     "valid explicit session",
			args:     []string{"hetzner-1", "--socket", "/tmp/amux/main", "--session", "lab", "--ssh", "host"},
			wantName: "hetzner-1",
			wantHost: config.Host{SSH: "host", SocketPath: "/tmp/amux/main", Session: "lab"},
		},
		{
			name:        "missing socket",
			args:        []string{"hetzner-1", "--ssh", "host"},
			wantErrText: remoteAddUsage,
		},
		{
			name:        "unknown flag",
			args:        []string{"hetzner-1", "--ssh", "host", "--socket", "/tmp/amux/main", "--bad"},
			wantErrText: remoteAddUsage,
		},
		{
			name:        "flag-like name",
			args:        []string{"--bad", "--ssh", "host", "--socket", "/tmp/amux/main"},
			wantErrText: remoteAddUsage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseRemoteAddArgs(tt.args)
			if tt.wantErrText != "" {
				if err == nil || err.Error() != tt.wantErrText {
					t.Fatalf("parseRemoteAddArgs(%v) error = %v, want %q", tt.args, err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRemoteAddArgs(%v): %v", tt.args, err)
			}
			if got.name != tt.wantName || got.host != tt.wantHost {
				t.Fatalf("parseRemoteAddArgs(%v) = %+v, want name %q host %+v", tt.args, got, tt.wantName, tt.wantHost)
			}
		})
	}
}

func TestRemoteLayoutPaneEntriesUsesWindowLeafOrder(t *testing.T) {
	t.Parallel()

	layout := &proto.LayoutSnapshot{
		Windows: []proto.WindowSnapshot{
			{
				Name:         "build",
				ActivePaneID: 3,
				LeadPaneID:   2,
				Root: proto.CellSnapshot{
					Dir: int(mux.SplitVertical),
					Children: []proto.CellSnapshot{
						{IsLeaf: true, PaneID: 2},
						{IsLeaf: true, PaneID: 3},
					},
				},
				Panes: []proto.PaneSnapshot{
					{ID: 3, Name: "right", Host: "remote"},
					{ID: 2, Name: "left", Host: "remote"},
					{ID: 9, Name: "hidden", Host: "remote"},
				},
			},
		},
	}

	entries := remoteLayoutPaneEntries(layout)
	if len(entries) != 2 {
		t.Fatalf("remoteLayoutPaneEntries len = %d, want 2: %+v", len(entries), entries)
	}
	if entries[0].Name != "left" || !entries[0].Lead || entries[0].Active {
		t.Fatalf("first entry = %+v, want leaf-order lead left", entries[0])
	}
	if entries[1].Name != "right" || !entries[1].Active || entries[1].Lead {
		t.Fatalf("second entry = %+v, want active right", entries[1])
	}
}

func TestRemoteGeometryForPane(t *testing.T) {
	t.Parallel()

	layout := &proto.LayoutSnapshot{
		Windows: []proto.WindowSnapshot{
			{
				Name:         "one",
				ActivePaneID: 1,
				Root:         leafCell(1, 80, 24),
				Panes:        []proto.PaneSnapshot{{ID: 1, Name: "agent"}},
			},
			{
				Name: "two",
				Root: leafCell(2, 80, 24),
				Panes: []proto.PaneSnapshot{
					{ID: 2, Name: "worker"},
				},
			},
		},
	}

	geo, err := remoteGeometryForPane(layout, "worker")
	if err != nil {
		t.Fatalf("remoteGeometryForPane(worker): %v", err)
	}
	if geo.id != 2 || geo.window != "two" || geo.cell.W != 80 || geo.cell.H != 24 {
		t.Fatalf("geometry = %+v, want pane 2 in window two at 80x24", geo)
	}

	ambiguous := *layout
	ambiguous.Windows[1].Panes[0].Name = "agent"
	if _, err := remoteGeometryForPane(&ambiguous, "agent"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous remoteGeometryForPane error = %v, want ambiguous", err)
	}
	if _, err := remoteGeometryForPane(nil, "agent"); err == nil || err.Error() != "remote layout is empty" {
		t.Fatalf("nil remoteGeometryForPane error = %v, want empty layout", err)
	}
}

func TestPlanRemoteResize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		geo         remotePaneGeometry
		cols        int
		rows        int
		want        []remoteResizeStep
		wantErrText string
	}{
		{
			name: "grow last column leftward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 20},
				path: []layoutPathStep{{dir: int(mux.SplitVertical), index: 1, count: 2}},
			},
			cols: 45,
			rows: mux.PaneContentHeight(20),
			want: []remoteResizeStep{{direction: "left", delta: 5}},
		},
		{
			name: "shrink first column leftward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 20},
				path: []layoutPathStep{{dir: int(mux.SplitVertical), index: 0, count: 2}},
			},
			cols: 35,
			rows: mux.PaneContentHeight(20),
			want: []remoteResizeStep{{direction: "left", delta: 5}},
		},
		{
			name: "grow first row downward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
				path: []layoutPathStep{{dir: int(mux.SplitHorizontal), index: 0, count: 2}},
			},
			cols: 40,
			rows: mux.PaneContentHeight(12) + 3,
			want: []remoteResizeStep{{direction: "down", delta: 3}},
		},
		{
			name: "already matches",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
				path: []layoutPathStep{{dir: int(mux.SplitHorizontal), index: 0, count: 2}},
			},
			cols: 40,
			rows: mux.PaneContentHeight(12),
		},
		{
			name: "missing horizontal donor",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
			},
			cols:        41,
			rows:        mux.PaneContentHeight(12),
			wantErrText: "remote pane agent cannot be resized horizontally",
		},
		{
			name: "invalid local size",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
			},
			cols:        0,
			rows:        mux.PaneContentHeight(12),
			wantErrText: "local mirror size is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := planRemoteResize(tt.geo, tt.cols, tt.rows)
			if tt.wantErrText != "" {
				if err == nil || err.Error() != tt.wantErrText {
					t.Fatalf("planRemoteResize() error = %v, want %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("planRemoteResize(): %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("planRemoteResize() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRemoteHostHealth(t *testing.T) {
	t.Parallel()

	snaps := []mirrorpkg.Snapshot{
		{RemoteRef: checkpoint.RemoteRef{Host: "one"}, State: mirrorpkg.StateConnected},
		{RemoteRef: checkpoint.RemoteRef{Host: "one"}, State: mirrorpkg.StateDead},
		{RemoteRef: checkpoint.RemoteRef{Host: "one"}, State: mirrorpkg.StateDead},
		{RemoteRef: checkpoint.RemoteRef{Host: "two"}, State: mirrorpkg.StateConnecting},
	}

	if got := remoteHostHealth("none", snaps); got != "idle" {
		t.Fatalf("remoteHostHealth(none) = %q, want idle", got)
	}
	if got := remoteHostHealth("one", snaps); got != "connected,dead(2)" {
		t.Fatalf("remoteHostHealth(one) = %q, want connected,dead(2)", got)
	}
}

func leafCell(id uint32, w, h int) proto.CellSnapshot {
	return proto.CellSnapshot{IsLeaf: true, PaneID: id, W: w, H: h}
}
