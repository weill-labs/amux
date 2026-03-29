package checkpoint

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestCrashRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	cp := &CrashCheckpoint{
		Version:       CrashVersion,
		SessionName:   "test-session",
		Counter:       5,
		WindowCounter: 2,
		Generation:    42,
		Layout: proto.LayoutSnapshot{
			SessionName:    "test-session",
			ActivePaneID:   2,
			Width:          80,
			Height:         23,
			ActiveWindowID: 1,
			Windows: []proto.WindowSnapshot{
				{
					ID:           1,
					Name:         "window-1",
					Index:        1,
					ActivePaneID: 2,
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
			},
		},
		PaneStates: []CrashPaneState{
			{
				ID:           1,
				Meta:         mux.PaneMeta{Name: "pane-1", Host: "local", Color: "f38ba8"},
				ManualBranch: true,
				Cols:         39,
				Rows:         22,
				History:      []string{"old-1", "old-2"},
				Screen:       "hello world",
				CreatedAt:    now,
				Cwd:          "/home/user/project",
			},
			{
				ID:        2,
				Meta:      mux.PaneMeta{Name: "pane-2", Host: "remote", Task: "TASK-1", Color: "a6e3a1"},
				Cols:      39,
				Rows:      22,
				History:   []string{"remote-old-1"},
				Screen:    "$ echo test\ntest",
				CreatedAt: now,
				IsProxy:   true,
			},
		},
		Timestamp: now,
	}
	setMetaCollections(t, &cp.PaneStates[0].Meta, []int{42, 73}, []string{"LAB-338", "LAB-412"})
	setMetaCollections(t, &cp.PaneStates[1].Meta, []int{99}, []string{"LAB-777"})

	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	if err := WriteCrash(cp, "test-session", startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	path := CrashCheckpointPathTimestamped("test-session", startTime)
	got, err := ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash: %v", err)
	}

	if got.Version != CrashVersion {
		t.Errorf("Version = %d, want %d", got.Version, CrashVersion)
	}
	if got.SessionName != cp.SessionName {
		t.Errorf("SessionName = %q, want %q", got.SessionName, cp.SessionName)
	}
	if got.Counter != cp.Counter {
		t.Errorf("Counter = %d, want %d", got.Counter, cp.Counter)
	}
	if got.WindowCounter != cp.WindowCounter {
		t.Errorf("WindowCounter = %d, want %d", got.WindowCounter, cp.WindowCounter)
	}
	if got.Generation != cp.Generation {
		t.Errorf("Generation = %d, want %d", got.Generation, cp.Generation)
	}
	if len(got.PaneStates) != len(cp.PaneStates) {
		t.Fatalf("PaneStates = %d, want %d", len(got.PaneStates), len(cp.PaneStates))
	}

	for i, want := range cp.PaneStates {
		got := got.PaneStates[i]
		if got.ID != want.ID {
			t.Errorf("PaneStates[%d].ID = %d, want %d", i, got.ID, want.ID)
		}
		if got.Meta.Name != want.Meta.Name {
			t.Errorf("PaneStates[%d].Meta.Name = %q, want %q", i, got.Meta.Name, want.Meta.Name)
		}
		if got.ManualBranch != want.ManualBranch {
			t.Errorf("PaneStates[%d].ManualBranch = %v, want %v", i, got.ManualBranch, want.ManualBranch)
		}
		if got.Cols != want.Cols {
			t.Errorf("PaneStates[%d].Cols = %d, want %d", i, got.Cols, want.Cols)
		}
		if got.Rows != want.Rows {
			t.Errorf("PaneStates[%d].Rows = %d, want %d", i, got.Rows, want.Rows)
		}
		if len(got.History) != len(want.History) {
			t.Errorf("PaneStates[%d].History len = %d, want %d", i, len(got.History), len(want.History))
		} else {
			for j := range want.History {
				if got.History[j] != want.History[j] {
					t.Errorf("PaneStates[%d].History[%d] = %q, want %q", i, j, got.History[j], want.History[j])
				}
			}
		}
		if got.Screen != want.Screen {
			t.Errorf("PaneStates[%d].Screen = %q, want %q", i, got.Screen, want.Screen)
		}
		if got.IsProxy != want.IsProxy {
			t.Errorf("PaneStates[%d].IsProxy = %v, want %v", i, got.IsProxy, want.IsProxy)
		}
		if got.Cwd != want.Cwd {
			t.Errorf("PaneStates[%d].Cwd = %q, want %q", i, got.Cwd, want.Cwd)
		}
		if !got.CreatedAt.Equal(want.CreatedAt) {
			t.Errorf("PaneStates[%d].CreatedAt = %v, want %v", i, got.CreatedAt, want.CreatedAt)
		}
		gotPRs, gotIssues := metaCollections(t, got.Meta)
		wantPRs, wantIssues := metaCollections(t, want.Meta)
		if !reflect.DeepEqual(gotPRs, wantPRs) {
			t.Errorf("PaneStates[%d].Meta.TrackedPRs = %v, want %v", i, gotPRs, wantPRs)
		}
		if !reflect.DeepEqual(gotIssues, wantIssues) {
			t.Errorf("PaneStates[%d].Meta.TrackedIssues = %v, want %v", i, gotIssues, wantIssues)
		}
	}

	// Verify layout was preserved
	if got.Layout.ActivePaneID != cp.Layout.ActivePaneID {
		t.Errorf("Layout.ActivePaneID = %d, want %d", got.Layout.ActivePaneID, cp.Layout.ActivePaneID)
	}
	if len(got.Layout.Windows) != 1 {
		t.Fatalf("Layout.Windows = %d, want 1", len(got.Layout.Windows))
	}
	if got.Layout.Windows[0].Name != "window-1" {
		t.Errorf("Layout.Windows[0].Name = %q, want %q", got.Layout.Windows[0].Name, "window-1")
	}
}

func TestCrashAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	cp := &CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "atomic-test",
		Timestamp:   time.Now(),
	}

	if err := WriteCrash(cp, "atomic-test", startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	path := CrashCheckpointPathTimestamped("atomic-test", startTime)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint file should exist: %v", err)
	}

	// Overwrite with new data — should be atomic (no partial writes)
	cp.Counter = 99
	if err := WriteCrash(cp, "atomic-test", startTime); err != nil {
		t.Fatalf("WriteCrash overwrite: %v", err)
	}

	got, err := ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash: %v", err)
	}
	if got.Counter != 99 {
		t.Errorf("Counter = %d, want 99 (atomic overwrite)", got.Counter)
	}
}

func TestCrashVersionValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	cp := &CrashCheckpoint{
		Version:     999, // invalid version
		SessionName: "bad-version",
	}

	if err := WriteCrash(cp, "bad-version", startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	path := CrashCheckpointPathTimestamped("bad-version", startTime)
	if _, err := ReadCrash(path); err == nil {
		t.Error("expected error reading checkpoint with invalid version")
	}
}

func TestReadCrashAcceptsOlderVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	cp := &CrashCheckpoint{
		Version:       CrashVersion - 1,
		SessionName:   "older-version",
		Counter:       7,
		WindowCounter: 2,
		Timestamp:     startTime,
	}

	if err := WriteCrash(cp, cp.SessionName, startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	got, err := ReadCrash(CrashCheckpointPathTimestamped(cp.SessionName, startTime))
	if err != nil {
		t.Fatalf("ReadCrash: %v", err)
	}
	if got.Version != cp.Version {
		t.Fatalf("Version = %d, want %d", got.Version, cp.Version)
	}
	if got.Counter != cp.Counter {
		t.Fatalf("Counter = %d, want %d", got.Counter, cp.Counter)
	}
}

func TestCrashCheckpointPathTimestamped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	startTime := time.Date(2026, time.March, 21, 12, 34, 56, 789, time.UTC)
	got := CrashCheckpointPathTimestamped("main", startTime)
	want := filepath.Join(dir, "amux", "20260321-123456_main.json")
	if got != want {
		t.Fatalf("CrashCheckpointPathTimestamped() = %q, want %q", got, want)
	}
}

func TestFindCrashCheckpointsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	checkpointDir := CrashCheckpointDir()
	if err := os.MkdirAll(checkpointDir, 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	session := "main"
	startTimes := []time.Time{
		time.Date(2026, time.March, 21, 12, 34, 54, 0, time.UTC),
		time.Date(2026, time.March, 21, 12, 34, 56, 0, time.UTC),
		time.Date(2026, time.March, 21, 12, 34, 55, 0, time.UTC),
	}
	for _, startTime := range startTimes {
		path := CrashCheckpointPathTimestamped(session, startTime)
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	otherPath := CrashCheckpointPathTimestamped("other", time.Date(2026, time.March, 21, 12, 34, 57, 0, time.UTC))
	if err := os.WriteFile(otherPath, []byte("{}"), 0600); err != nil {
		t.Fatalf("WriteFile(%q): %v", otherPath, err)
	}

	got := FindCrashCheckpoints(session)
	want := []string{
		CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 56, 0, time.UTC)),
		CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 55, 0, time.UTC)),
		CrashCheckpointPathTimestamped(session, time.Date(2026, time.March, 21, 12, 34, 54, 0, time.UTC)),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FindCrashCheckpoints() = %v, want %v", got, want)
	}
}

func TestCrashRemoveFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	cp := &CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "remove-test",
	}

	if err := WriteCrash(cp, "remove-test", startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	path := CrashCheckpointPathTimestamped("remove-test", startTime)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checkpoint should exist: %v", err)
	}

	if err := RemoveCrashFile(path); err != nil {
		t.Fatalf("RemoveCrashFile: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("checkpoint file should be removed")
	}
}

func TestCrashCheckpointDir(t *testing.T) {
	// Cannot use t.Parallel() — subtests use t.Setenv which modifies process env.

	t.Run("default", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "")
		dir := CrashCheckpointDir()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".local", "state", "amux")
		if dir != want {
			t.Errorf("CrashCheckpointDir() = %q, want %q", dir, want)
		}
	})

	t.Run("xdg_override", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", "/tmp/test-xdg-state")
		dir := CrashCheckpointDir()
		want := "/tmp/test-xdg-state/amux"
		if dir != want {
			t.Errorf("CrashCheckpointDir() = %q, want %q", dir, want)
		}
	})
}

func TestReadCrashDoesNotDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	startTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

	cp := &CrashCheckpoint{
		Version:     CrashVersion,
		SessionName: "persist-test",
	}

	if err := WriteCrash(cp, "persist-test", startTime); err != nil {
		t.Fatalf("WriteCrash: %v", err)
	}

	path := CrashCheckpointPathTimestamped("persist-test", startTime)

	// Read should NOT delete the file (unlike hot-reload checkpoint)
	if _, err := ReadCrash(path); err != nil {
		t.Fatalf("first ReadCrash: %v", err)
	}
	if _, err := ReadCrash(path); err != nil {
		t.Fatalf("second ReadCrash should succeed (file not deleted): %v", err)
	}
}
