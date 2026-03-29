package checkpoint

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	cp := &ServerCheckpoint{
		Version:     ServerCheckpointVersion,
		SessionName: "test-session",
		StartedAt:   time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC),
		Counter:     5,
		ListenerFd:  10,
		Layout: proto.LayoutSnapshot{
			SessionName:  "test-session",
			ActivePaneID: 2,
			Width:        80,
			Height:       23,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23, Dir: 0,
				Children: []proto.CellSnapshot{
					{X: 0, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 1},
					{X: 40, Y: 0, W: 39, H: 23, IsLeaf: true, Dir: -1, PaneID: 2},
				},
			},
			Panes: []proto.PaneSnapshot{
				{ID: 1, Name: "pane-1", Host: "local", Color: "f38ba8"},
				{ID: 2, Name: "pane-2", Host: "remote", Task: "TASK-1", Color: "a6e3a1"},
			},
		},
		Panes: []PaneCheckpoint{
			{
				ID:           1,
				Meta:         mux.PaneMeta{Name: "pane-1", Host: "local", Color: "f38ba8"},
				ManualBranch: true,
				PtmxFd:       5,
				PID:          1234,
				Cols:         39,
				Rows:         22,
				History: []string{
					"old-1",
					"old-2",
				},
				Screen: "hello world",
			},
			{
				ID:     2,
				Meta:   mux.PaneMeta{Name: "pane-2", Host: "remote", Task: "TASK-1", Color: "a6e3a1"},
				PtmxFd: 7,
				PID:    5678,
				Cols:   39,
				Rows:   22,
				History: []string{
					"remote-old-1",
				},
				Screen: "$ echo test\ntest",
			},
		},
	}
	setMetaCollections(t, &cp.Panes[0].Meta, []int{42, 73}, []string{"LAB-338", "LAB-412"})
	setMetaCollections(t, &cp.Panes[1].Meta, []int{99}, []string{"LAB-777"})

	path, err := Write(cp)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got.SessionName != cp.SessionName {
		t.Errorf("SessionName = %q, want %q", got.SessionName, cp.SessionName)
	}
	if got.Counter != cp.Counter {
		t.Errorf("Counter = %d, want %d", got.Counter, cp.Counter)
	}
	if got.Version != cp.Version {
		t.Errorf("Version = %d, want %d", got.Version, cp.Version)
	}
	if !got.StartedAt.Equal(cp.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, cp.StartedAt)
	}
	if got.ListenerFd != cp.ListenerFd {
		t.Errorf("ListenerFd = %d, want %d", got.ListenerFd, cp.ListenerFd)
	}
	if len(got.Panes) != len(cp.Panes) {
		t.Fatalf("Panes = %d, want %d", len(got.Panes), len(cp.Panes))
	}

	for i, want := range cp.Panes {
		got := got.Panes[i]
		if got.ID != want.ID {
			t.Errorf("Pane[%d].ID = %d, want %d", i, got.ID, want.ID)
		}
		if got.Meta.Name != want.Meta.Name {
			t.Errorf("Pane[%d].Meta.Name = %q, want %q", i, got.Meta.Name, want.Meta.Name)
		}
		if got.ManualBranch != want.ManualBranch {
			t.Errorf("Pane[%d].ManualBranch = %v, want %v", i, got.ManualBranch, want.ManualBranch)
		}
		if got.PtmxFd != want.PtmxFd {
			t.Errorf("Pane[%d].PtmxFd = %d, want %d", i, got.PtmxFd, want.PtmxFd)
		}
		if got.PID != want.PID {
			t.Errorf("Pane[%d].PID = %d, want %d", i, got.PID, want.PID)
		}
		if got.Cols != want.Cols {
			t.Errorf("Pane[%d].Cols = %d, want %d", i, got.Cols, want.Cols)
		}
		if got.Rows != want.Rows {
			t.Errorf("Pane[%d].Rows = %d, want %d", i, got.Rows, want.Rows)
		}
		if len(got.History) != len(want.History) {
			t.Fatalf("Pane[%d].History len = %d, want %d", i, len(got.History), len(want.History))
		}
		for j := range want.History {
			if got.History[j] != want.History[j] {
				t.Errorf("Pane[%d].History[%d] = %q, want %q", i, j, got.History[j], want.History[j])
			}
		}
		if got.Screen != want.Screen {
			t.Errorf("Pane[%d].Screen = %q, want %q", i, got.Screen, want.Screen)
		}
		gotPRs, gotIssues := metaCollections(t, got.Meta)
		wantPRs, wantIssues := metaCollections(t, want.Meta)
		if !reflect.DeepEqual(gotPRs, wantPRs) {
			t.Errorf("Pane[%d].Meta.TrackedPRs = %v, want %v", i, gotPRs, wantPRs)
		}
		if !reflect.DeepEqual(gotIssues, wantIssues) {
			t.Errorf("Pane[%d].Meta.TrackedIssues = %v, want %v", i, gotIssues, wantIssues)
		}
	}

	// Verify layout was preserved
	if got.Layout.ActivePaneID != cp.Layout.ActivePaneID {
		t.Errorf("Layout.ActivePaneID = %d, want %d", got.Layout.ActivePaneID, cp.Layout.ActivePaneID)
	}
	if len(got.Layout.Root.Children) != len(cp.Layout.Root.Children) {
		t.Errorf("Layout.Root.Children = %d, want %d", len(got.Layout.Root.Children), len(cp.Layout.Root.Children))
	}
}

func TestReadRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()

	path, err := Write(&ServerCheckpoint{
		Version:     ServerCheckpointVersion - 1,
		SessionName: "unsupported",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "unsupported checkpoint version") {
		t.Fatalf("Read() error = %v, want unsupported checkpoint version", err)
	}
}

func TestReadReturnsCheckpointOnUnsupportedVersion(t *testing.T) {
	t.Parallel()

	path, err := Write(&ServerCheckpoint{
		Version:     ServerCheckpointVersion - 1,
		SessionName: "unsupported",
		ListenerFd:  17,
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	cp, err := Read(path)
	if err == nil {
		t.Fatal("Read() error = nil, want unsupported checkpoint version")
	}

	var versionErr UnsupportedServerCheckpointVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("Read() error = %T %v, want UnsupportedServerCheckpointVersionError", err, err)
	}
	if got, want := versionErr.Error(), "unsupported checkpoint version 1 (want 2)"; got != want {
		t.Fatalf("UnsupportedServerCheckpointVersionError.Error() = %q, want %q", got, want)
	}
	if cp == nil {
		t.Fatal("Read() checkpoint = nil, want decoded checkpoint on version error")
	}
	if cp.SessionName != "unsupported" || cp.ListenerFd != 17 {
		t.Fatalf("checkpoint = %+v, want session unsupported with listener fd 17", cp)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("checkpoint file should be removed after version error, stat err = %v", statErr)
	}
}

func TestReadReturnsNilOnDecodeError(t *testing.T) {
	t.Parallel()

	// Write a file with invalid gob content to trigger a decode error.
	f, err := os.CreateTemp("", "amux-corrupt-*.gob")
	if err != nil {
		t.Fatalf("os.CreateTemp: %v", err)
	}
	path := f.Name()
	if _, err := f.WriteString("this is not valid gob data"); err != nil {
		f.Close()
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	cp, err := Read(path)
	if err == nil {
		t.Fatal("Read() error = nil, want decode error")
	}
	if cp != nil {
		t.Fatalf("Read() checkpoint = %+v, want nil on decode error", cp)
	}
	if !strings.Contains(err.Error(), "decoding checkpoint") {
		t.Fatalf("Read() error = %v, want decode error context", err)
	}
	// File must be consumed even when decoding fails.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("checkpoint file should be removed after decode error, stat err = %v", statErr)
	}
}

func TestReadDeletesFile(t *testing.T) {
	t.Parallel()

	cp := &ServerCheckpoint{Version: ServerCheckpointVersion, SessionName: "delete-test"}
	path, err := Write(cp)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := Read(path); err != nil {
		t.Fatalf("Read: %v", err)
	}

	// File should be deleted after Read
	if _, err := Read(path); err == nil {
		t.Error("expected error reading deleted checkpoint file")
	}
}

func TestWriteEmptyCheckpoint(t *testing.T) {
	t.Parallel()

	cp := &ServerCheckpoint{Version: ServerCheckpointVersion, SessionName: "empty"}
	path, err := Write(cp)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.SessionName != "empty" {
		t.Errorf("SessionName = %q, want %q", got.SessionName, "empty")
	}
	if len(got.Panes) != 0 {
		t.Errorf("Panes = %d, want 0", len(got.Panes))
	}
}

func setMetaCollections(t *testing.T, meta *mux.PaneMeta, prs []int, issues []string) {
	t.Helper()

	meta.TrackedPRs = make([]proto.TrackedPR, 0, len(prs))
	for _, pr := range prs {
		meta.TrackedPRs = append(meta.TrackedPRs, proto.TrackedPR{Number: pr})
	}
	meta.TrackedIssues = make([]proto.TrackedIssue, 0, len(issues))
	for _, issue := range issues {
		meta.TrackedIssues = append(meta.TrackedIssues, proto.TrackedIssue{ID: issue})
	}
}

func metaCollections(t *testing.T, meta mux.PaneMeta) ([]int, []string) {
	t.Helper()

	return proto.TrackedPRNumbers(meta.TrackedPRs), proto.TrackedIssueIDs(meta.TrackedIssues)
}
