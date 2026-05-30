package server

import (
	"errors"
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

func TestRunRemoteCommandUsageErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing subcommand", want: remoteCommandUsage},
		{name: "unknown subcommand", args: []string{"bogus"}, want: remoteCommandUsage},
		{name: "add usage", args: []string{"add"}, want: remoteAddUsage},
		{name: "list usage", args: []string{"list", "extra"}, want: remoteListUsage},
		{name: "status usage", args: []string{"status", "extra"}, want: remoteStatusUsage},
		{name: "rm usage", args: []string{"rm"}, want: remoteRmUsage},
		{name: "panes usage", args: []string{"panes"}, want: remotePanesUsage},
		{name: "attach usage", args: []string{"attach"}, want: remoteAttachUsage},
		{name: "attach malformed target", args: []string{"attach", "remote:"}, want: remoteAttachUsage},
		{name: "attach flag-like chooser host", args: []string{"attach", "--bad"}, want: remoteAttachUsage},
		{name: "detach usage", args: []string{"detach"}, want: remoteDetachUsage},
		{name: "resize usage", args: []string{"resize"}, want: remoteResizeUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := runRemoteCommand(&CommandContext{Args: tt.args})
			if got.Err == nil || got.Err.Error() != tt.want {
				t.Fatalf("runRemoteCommand(%v) error = %v, want %q", tt.args, got.Err, tt.want)
			}
		})
	}
}

func TestRunRemoteAttachChooserRequiresAttachedClient(t *testing.T) {
	t.Parallel()

	got := runRemoteCommand(&CommandContext{Args: []string{"attach", "remote"}})
	if got.Err == nil || got.Err.Error() != "no client attached" {
		t.Fatalf("runRemoteCommand attach chooser error = %v, want no client attached", got.Err)
	}
}

type fakeChooserSender struct {
	sent    []*Message
	sendErr error
	flushed bool
}

func (f *fakeChooserSender) Send(m *Message) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeChooserSender) Flush() error {
	f.flushed = true
	return nil
}

func TestSendRemoteChooser(t *testing.T) {
	t.Parallel()

	populated := &proto.LayoutSnapshot{
		Windows: []proto.WindowSnapshot{{
			Name:  "w",
			Root:  proto.CellSnapshot{IsLeaf: true, PaneID: 1},
			Panes: []proto.PaneSnapshot{{ID: 1, Name: "remote-agent", Host: "remote"}},
		}},
	}

	// Empty remote layout → error, nothing sent (silent no-op guard).
	empty := &fakeChooserSender{}
	if r := sendRemoteChooser(empty, &proto.LayoutSnapshot{}, "hetzner-1"); r.Err == nil || r.Err.Error() != "no remote panes on hetzner-1" {
		t.Fatalf("empty layout result.Err = %v, want \"no remote panes on hetzner-1\"", r.Err)
	}
	if len(empty.sent) != 0 || empty.flushed {
		t.Fatalf("empty layout must not send/flush: sent=%d flushed=%v", len(empty.sent), empty.flushed)
	}

	// Send failure → propagated.
	failing := &fakeChooserSender{sendErr: errors.New("send boom")}
	if r := sendRemoteChooser(failing, populated, "hetzner-1"); r.Err == nil || r.Err.Error() != "send boom" {
		t.Fatalf("send-error result.Err = %v, want \"send boom\"", r.Err)
	}

	// Populated layout → chooser pushed, flushed, success output.
	ok := &fakeChooserSender{}
	r := sendRemoteChooser(ok, populated, "hetzner-1")
	if r.Err != nil || !strings.Contains(r.Output, "Opened remote pane chooser for hetzner-1") {
		t.Fatalf("populated result = %+v, want success output", r)
	}
	if len(ok.sent) != 1 || !ok.flushed {
		t.Fatalf("populated must send once + flush: sent=%d flushed=%v", len(ok.sent), ok.flushed)
	}
	if msg := ok.sent[0]; msg.Type != MsgTypeChooser || msg.Chooser == nil ||
		msg.Chooser.Kind != proto.ChooserKindRemotePanes || msg.Chooser.Host != "hetzner-1" || msg.Chooser.Layout != populated {
		t.Fatalf("sent message = %+v, want MsgTypeChooser remote-panes for hetzner-1 carrying the layout", ok.sent[0])
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

func TestRemoteLayoutPaneEntriesLegacyLayout(t *testing.T) {
	t.Parallel()

	layout := &proto.LayoutSnapshot{
		ActivePaneID: 2,
		LeadPaneID:   1,
		Root: proto.CellSnapshot{
			Dir: int(mux.SplitVertical),
			Children: []proto.CellSnapshot{
				{IsLeaf: true, PaneID: 1},
				{IsLeaf: true, PaneID: 2},
				{IsLeaf: true, PaneID: 99},
			},
		},
		Panes: []proto.PaneSnapshot{
			{ID: 2, Name: "right", Host: "remote"},
			{ID: 1, Name: "left", Host: "remote"},
		},
	}

	entries := remoteLayoutPaneEntries(layout)
	if len(entries) != 2 {
		t.Fatalf("remoteLayoutPaneEntries len = %d, want 2: %+v", len(entries), entries)
	}
	if entries[0].Name != "left" || !entries[0].Lead || entries[0].Active {
		t.Fatalf("first legacy entry = %+v, want lead left", entries[0])
	}
	if entries[1].Name != "right" || !entries[1].Active || entries[1].Lead {
		t.Fatalf("second legacy entry = %+v, want active right", entries[1])
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

	legacy := &proto.LayoutSnapshot{
		ActivePaneID: 7,
		LeadPaneID:   7,
		Root:         leafCell(7, 100, 30),
		Panes:        []proto.PaneSnapshot{{ID: 7, Name: "legacy"}},
	}
	geo, err = remoteGeometryForPane(legacy, "legacy")
	if err != nil {
		t.Fatalf("legacy remoteGeometryForPane: %v", err)
	}
	if geo.id != 7 || !geo.active || !geo.lead || geo.cell.W != 100 || geo.cell.H != 30 {
		t.Fatalf("legacy geometry = %+v, want active lead pane 7 at 100x30", geo)
	}
	if _, err := remoteGeometryForPane(legacy, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing remoteGeometryForPane error = %v, want not found", err)
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
			name: "grow first column rightward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 20},
				path: []layoutPathStep{{dir: int(mux.SplitVertical), index: 0, count: 2}},
			},
			cols: 45,
			rows: mux.PaneContentHeight(20),
			want: []remoteResizeStep{{direction: "right", delta: 5}},
		},
		{
			name: "shrink last column rightward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 20},
				path: []layoutPathStep{{dir: int(mux.SplitVertical), index: 1, count: 2}},
			},
			cols: 35,
			rows: mux.PaneContentHeight(20),
			want: []remoteResizeStep{{direction: "right", delta: 5}},
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
			name: "grow last row upward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
				path: []layoutPathStep{{dir: int(mux.SplitHorizontal), index: 1, count: 2}},
			},
			cols: 40,
			rows: mux.PaneContentHeight(12) + 3,
			want: []remoteResizeStep{{direction: "up", delta: 3}},
		},
		{
			name: "shrink first row upward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
				path: []layoutPathStep{{dir: int(mux.SplitHorizontal), index: 0, count: 2}},
			},
			cols: 40,
			rows: mux.PaneContentHeight(12) - 3,
			want: []remoteResizeStep{{direction: "up", delta: 3}},
		},
		{
			name: "shrink last row downward",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
				path: []layoutPathStep{{dir: int(mux.SplitHorizontal), index: 1, count: 2}},
			},
			cols: 40,
			rows: mux.PaneContentHeight(12) - 3,
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
			name: "missing vertical donor",
			geo: remotePaneGeometry{
				name: "agent",
				cell: proto.CellSnapshot{W: 40, H: 12},
			},
			cols:        40,
			rows:        mux.PaneContentHeight(12) + 1,
			wantErrText: "remote pane agent cannot be resized vertically",
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

func TestRemoteCommandSession(t *testing.T) {
	t.Parallel()

	if got := remoteCommandSession(config.Host{}); got != DefaultSessionName {
		t.Fatalf("remoteCommandSession(empty) = %q, want %q", got, DefaultSessionName)
	}
	if got := remoteCommandSession(config.Host{Session: " lab "}); got != "lab" {
		t.Fatalf("remoteCommandSession(trimmed) = %q, want lab", got)
	}
}

func TestRemoteMirrorSnapshotsNilContext(t *testing.T) {
	t.Parallel()

	if got := remoteMirrorSnapshots(nil); got != nil {
		t.Fatalf("remoteMirrorSnapshots(nil) = %+v, want nil", got)
	}
	if got := remoteMirrorSnapshots(&CommandContext{}); got != nil {
		t.Fatalf("remoteMirrorSnapshots(empty context) = %+v, want nil", got)
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

func TestFormatRemoteStatus(t *testing.T) {
	t.Parallel()

	hosts := map[string]config.Host{
		"hetzner-1": {SSH: "host1", SocketPath: "/tmp/a/main"},
		"hetzner-2": {SSH: "host2", SocketPath: "/tmp/b/main"},
	}
	snaps := []mirrorpkg.Snapshot{
		{RemoteRef: checkpoint.RemoteRef{Host: "hetzner-1", PaneName: "pane-1786"}, RemotePaneID: 1786, State: mirrorpkg.StateConnected},
		{RemoteRef: checkpoint.RemoteRef{Host: "hetzner-1", PaneName: "L0-meta"}, State: mirrorpkg.StateReconnecting, LastError: "dial timeout"},
	}

	got := formatRemoteStatus(hosts, snaps)

	// Header present.
	if !strings.Contains(got, "HOST") || !strings.Contains(got, "STATE") {
		t.Fatalf("missing header:\n%s", got)
	}
	// Mirror rows sorted by remote pane name (L0-meta before pane-1786) with
	// the remote pane ID and last-error annotation rendered.
	if idx1, idx2 := strings.Index(got, "L0-meta"), strings.Index(got, "pane-1786"); idx1 < 0 || idx2 < 0 || idx1 > idx2 {
		t.Fatalf("mirror rows missing or unsorted:\n%s", got)
	}
	if !strings.Contains(got, "1786") {
		t.Fatalf("remote pane id not rendered:\n%s", got)
	}
	if !strings.Contains(got, "reconnecting (dial timeout)") {
		t.Fatalf("state+error not rendered:\n%s", got)
	}
	// A host with no active mirrors renders an idle placeholder row.
	if !strings.Contains(got, "hetzner-2") {
		t.Fatalf("idle host missing:\n%s", got)
	}
}

func TestRemotePaneIDLabel(t *testing.T) {
	t.Parallel()

	if got := remotePaneIDLabel(0); got != "-" {
		t.Fatalf("remotePaneIDLabel(0) = %q, want -", got)
	}
	if got := remotePaneIDLabel(1786); got != "1786" {
		t.Fatalf("remotePaneIDLabel(1786) = %q, want 1786", got)
	}
}

func leafCell(id uint32, w, h int) proto.CellSnapshot {
	return proto.CellSnapshot{IsLeaf: true, PaneID: id, W: w, H: h}
}
