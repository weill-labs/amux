package mux

import (
	"testing"
)

func TestContentLines(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 5)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write two lines of content
	emu.Write([]byte("hello world\r\nline two\r\n"))

	lines := p.ContentLines()

	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (pane height), got %d", len(lines))
	}
	if lines[0] != "hello world" {
		t.Errorf("line 0: got %q, want %q", lines[0], "hello world")
	}
	if lines[1] != "line two" {
		t.Errorf("line 1: got %q, want %q", lines[1], "line two")
	}
	// Remaining lines should be empty
	for i := 2; i < 5; i++ {
		if lines[i] != "" {
			t.Errorf("line %d: got %q, want empty", i, lines[i])
		}
	}
}

func TestContentLinesStripsANSI(t *testing.T) {
	emu := NewVTEmulatorWithDrain(40, 3)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	// Write colored text
	emu.Write([]byte("\033[31mRED\033[m normal\r\n"))

	lines := p.ContentLines()

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "RED normal" {
		t.Errorf("line 0: got %q, want %q", lines[0], "RED normal")
	}
}

func TestCaptureSnapshotIncludesHistoryContentAndCursor(t *testing.T) {
	emu := NewVTEmulatorWithDrain(12, 2)

	p := &Pane{
		ID:       1,
		emulator: emu,
	}
	p.SetRetainedHistory([]string{"base-1"})

	emu.Write([]byte("line-1\r\nline-2\r\nline-3"))

	snap := p.CaptureSnapshot()

	if got := snap.History; len(got) != 2 || got[0] != "base-1" || got[1] != "line-1" {
		t.Fatalf("History = %#v, want [base-1 line-1]", got)
	}
	if got := snap.Content; len(got) != 2 || got[0] != "line-2" || got[1] != "line-3" {
		t.Fatalf("Content = %#v, want [line-2 line-3]", got)
	}
	if snap.CursorCol != len("line-3") || snap.CursorRow != 1 {
		t.Fatalf("Cursor = (%d,%d), want (%d,1)", snap.CursorCol, snap.CursorRow, len("line-3"))
	}
	if snap.CursorHidden {
		t.Fatal("CursorHidden = true, want false")
	}
}

func TestApplyCwdBranchUpdatesAutoDetectedFields(t *testing.T) {
	t.Parallel()

	p := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1"}}

	p.ApplyCwdBranch("/home/user/repo", "main")

	if p.LiveCwd() != "/home/user/repo" {
		t.Fatalf("LiveCwd() = %q, want %q", p.LiveCwd(), "/home/user/repo")
	}
	if p.Meta.GitBranch != "main" {
		t.Fatalf("GitBranch = %q, want %q", p.Meta.GitBranch, "main")
	}
}

func TestApplyCwdBranchRespectsManualOverride(t *testing.T) {
	t.Parallel()

	p := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1", GitBranch: "manual-branch"}}
	p.SetMetaManualBranch(true)

	p.ApplyCwdBranch("/tmp", "auto-branch")

	if p.LiveCwd() != "/tmp" {
		t.Fatalf("LiveCwd() = %q, want CWD to always update", p.LiveCwd())
	}
	if p.Meta.GitBranch != "manual-branch" {
		t.Fatalf("GitBranch = %q, want manual override preserved", p.Meta.GitBranch)
	}
}

func TestSetMetaManualBranchToggle(t *testing.T) {
	t.Parallel()

	p := &Pane{ID: 1, Meta: PaneMeta{Name: "pane-1"}}

	// Initially auto-detect works
	p.ApplyCwdBranch("/tmp", "auto")
	if p.Meta.GitBranch != "auto" {
		t.Fatalf("GitBranch = %q, want auto", p.Meta.GitBranch)
	}

	// Set manual
	p.SetMetaManualBranch(true)
	p.Meta.GitBranch = "manual"
	p.ApplyCwdBranch("/tmp", "auto-2")
	if p.Meta.GitBranch != "manual" {
		t.Fatalf("GitBranch = %q, want manual preserved", p.Meta.GitBranch)
	}

	// Clear manual
	p.SetMetaManualBranch(false)
	p.ApplyCwdBranch("/tmp", "auto-3")
	if p.Meta.GitBranch != "auto-3" {
		t.Fatalf("GitBranch = %q, want auto-detect resumed", p.Meta.GitBranch)
	}
}

func TestSetOnMetaUpdateCallback(t *testing.T) {
	t.Parallel()

	p := &Pane{ID: 42}
	var received []MetaUpdate
	p.SetOnMetaUpdate(func(paneID uint32, update MetaUpdate) {
		if paneID != 42 {
			t.Errorf("paneID = %d, want 42", paneID)
		}
		received = append(received, update)
	})

	task := "build"
	seq := FormatMetaSequence(MetaUpdate{Task: &task})

	// Feed the escape sequence through the scanner directly
	updates := p.metaScanner.Scan(seq)
	for _, u := range updates {
		p.onMetaUpdate(p.ID, u)
	}

	if len(received) != 1 {
		t.Fatalf("received %d callbacks, want 1", len(received))
	}
	if *received[0].Task != "build" {
		t.Fatalf("task = %q, want %q", *received[0].Task, "build")
	}
}

func TestCaptureSnapshotRespectsScrollbackLimit(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(12, 2, 2)

	p := &Pane{
		ID:              1,
		emulator:        emu,
		scrollbackLines: 2,
	}
	p.SetRetainedHistory([]string{"base-1", "base-2", "base-3"})

	emu.Write([]byte("line-1\r\nline-2\r\nline-3"))

	snap := p.CaptureSnapshot()

	if got := snap.History; len(got) != 2 || got[0] != "base-3" || got[1] != "line-1" {
		t.Fatalf("History = %#v, want [base-3 line-1]", got)
	}
	if got := snap.Content; len(got) != 2 || got[0] != "line-2" || got[1] != "line-3" {
		t.Fatalf("Content = %#v, want [line-2 line-3]", got)
	}
}
