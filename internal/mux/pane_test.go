package mux

import (
	"fmt"
	"image/color"
	"testing"
)

func paneTestHexColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("%02x%02x%02x", uint8(r>>8), uint8(g>>8), uint8(b>>8))
}

func TestContentLines(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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

func TestTerminalSnapshotIncludesCursorAndMetadata(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrain(12, 2)
	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	emu.Write([]byte(
		"\x1b]10;#112233\x07" +
			"\x1b]11;#445566\x07" +
			"\x1b]12;#778899\x07" +
			"\x1b]8;;https://example.com\x07" +
			"\x1b[6 q" +
			"\x1b[?1049h" +
			"prompt",
	))

	snap := p.TerminalSnapshot()

	if snap.CursorCol != len("prompt") || snap.CursorRow != 0 {
		t.Fatalf("Cursor = (%d,%d), want (%d,0)", snap.CursorCol, snap.CursorRow, len("prompt"))
	}
	if snap.CursorHidden {
		t.Fatal("CursorHidden = true, want false")
	}
	if snap.Terminal.CursorStyle != "bar" || snap.Terminal.CursorBlinking {
		t.Fatalf("cursor style = (%q,%t), want (bar,false)", snap.Terminal.CursorStyle, snap.Terminal.CursorBlinking)
	}
	if !snap.Terminal.AltScreen {
		t.Fatal("AltScreen = false, want true")
	}
	if got := paneTestHexColor(snap.Terminal.ForegroundColor); got != "112233" {
		t.Fatalf("ForegroundColor = %q, want 112233", got)
	}
	if got := paneTestHexColor(snap.Terminal.BackgroundColor); got != "445566" {
		t.Fatalf("BackgroundColor = %q, want 445566", got)
	}
	if got := paneTestHexColor(snap.Terminal.CursorColor); got != "778899" {
		t.Fatalf("CursorColor = %q, want 778899", got)
	}
	if snap.Terminal.HyperlinkURL != "https://example.com" {
		t.Fatalf("HyperlinkURL = %q, want https://example.com", snap.Terminal.HyperlinkURL)
	}
	if got := len(snap.Terminal.Palette); got != 256 {
		t.Fatalf("palette len = %d, want 256", got)
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

func TestNewProxyPaneWithScrollbackDoesNotPinTypedGitBranch(t *testing.T) {
	t.Parallel()

	p := NewProxyPaneWithScrollback(1, PaneMeta{
		Name:      "pane-1",
		GitBranch: "restored-branch",
	}, 80, 24, DefaultScrollbackLines, nil, nil, nil)

	p.ApplyCwdBranch("/tmp/project", "auto-branch")

	if p.MetaManualBranch() {
		t.Fatal("MetaManualBranch() = true, want false for typed GitBranch without kv override")
	}
	if p.Meta.GitBranch != "auto-branch" {
		t.Fatalf("GitBranch = %q, want auto-branch", p.Meta.GitBranch)
	}
}

func TestNewProxyPaneWithScrollbackPinsExplicitBranchKV(t *testing.T) {
	t.Parallel()

	p := NewProxyPaneWithScrollback(1, PaneMeta{
		Name: "pane-1",
		KV: map[string]string{
			PaneMetaKeyBranch: "manual-branch",
		},
	}, 80, 24, DefaultScrollbackLines, nil, nil, nil)

	p.ApplyCwdBranch("/tmp/project", "auto-branch")

	if !p.MetaManualBranch() {
		t.Fatal("MetaManualBranch() = false, want true for explicit kv branch")
	}
	if p.Meta.GitBranch != "manual-branch" {
		t.Fatalf("GitBranch = %q, want manual-branch", p.Meta.GitBranch)
	}
}

func TestNewProxyPaneWithScrollbackPinsExplicitEmptyBranchKV(t *testing.T) {
	t.Parallel()

	p := NewProxyPaneWithScrollback(1, PaneMeta{
		Name: "pane-1",
		KV: map[string]string{
			PaneMetaKeyBranch: "",
		},
	}, 80, 24, DefaultScrollbackLines, nil, nil, nil)

	p.ApplyCwdBranch("/tmp/project", "auto-branch")

	if !p.MetaManualBranch() {
		t.Fatal("MetaManualBranch() = false, want true for explicit empty branch kv")
	}
	if p.Meta.GitBranch != "" {
		t.Fatalf("GitBranch = %q, want empty explicit override preserved", p.Meta.GitBranch)
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

func TestCaptureSnapshotIncludesCursorBlock(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrain(6, 2)
	p := &Pane{
		ID:       1,
		emulator: emu,
	}

	emu.Write([]byte("\x1b[2J\x1b[H❯ \x1b[7m \x1b[m\x1b[?25l\x1b[2;1H"))

	snap := p.CaptureSnapshot()
	if !snap.HasCursorBlock {
		t.Fatal("HasCursorBlock = false, want true")
	}
	if snap.CursorBlockCol != 2 || snap.CursorBlockRow != 0 {
		t.Fatalf("cursor block = (%d,%d), want (2,0)", snap.CursorBlockCol, snap.CursorBlockRow)
	}

	col, row, ok := p.CursorBlockPos()
	if !ok || col != 2 || row != 0 {
		t.Fatalf("CursorBlockPos() = (%d,%d,%t), want (2,0,true)", col, row, ok)
	}
}

func TestCaptureSnapshotTracksLiveScrollbackSourceWidthsAcrossResize(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(20, 1, 4)
	p := &Pane{
		ID:              1,
		emulator:        emu,
		scrollbackLines: 4,
		scrollbackLimit: 4,
	}
	wireScrollbackCallbacks(p)

	emu.Write([]byte("01234567890123456789\r\n"))
	emu.Resize(10, 1)
	emu.Write([]byte("ABCDEFGHIJ\r\n"))

	snap := p.CaptureSnapshot()
	if len(snap.LiveHistory) != 2 {
		t.Fatalf("LiveHistory len = %d, want 2", len(snap.LiveHistory))
	}
	if got := snap.LiveHistory[0]; got.Text != "01234567890123456789" || got.SourceWidth != 20 {
		t.Fatalf("LiveHistory[0] = %#v, want text=%q width=20", got, "01234567890123456789")
	}
	if got := snap.LiveHistory[1]; got.Text != "ABCDEFGHIJ" || got.SourceWidth != 10 {
		t.Fatalf("LiveHistory[1] = %#v, want text=%q width=10", got, "ABCDEFGHIJ")
	}
}

func TestPaneResetStateClearsRetainedAndLiveHistory(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(12, 2, 4)
	p := &Pane{
		ID:              1,
		emulator:        emu,
		scrollbackLines: 4,
	}
	p.SetRetainedHistory([]string{"base-1", "base-2"})

	emu.Write([]byte("line-1\r\nline-2\r\nline-3"))

	before := p.CaptureSnapshot()
	if len(before.History) == 0 {
		t.Fatal("history should be populated before reset")
	}
	if len(before.Content) == 0 || before.Content[0] == "" {
		t.Fatal("content should be populated before reset")
	}

	p.ResetState()

	after := p.CaptureSnapshot()
	if len(after.BaseHistory) != 0 {
		t.Fatalf("BaseHistory after reset = %#v, want empty", after.BaseHistory)
	}
	if len(after.LiveHistory) != 0 {
		t.Fatalf("LiveHistory after reset = %#v, want empty", after.LiveHistory)
	}
	if len(after.History) != 0 {
		t.Fatalf("History after reset = %#v, want empty", after.History)
	}
	if got := after.Content; len(got) != 2 || got[0] != "" || got[1] != "" {
		t.Fatalf("Content after reset = %#v, want blank rows", got)
	}
	if after.CursorCol != 0 || after.CursorRow != 0 {
		t.Fatalf("Cursor after reset = (%d,%d), want (0,0)", after.CursorCol, after.CursorRow)
	}
	if after.CursorHidden {
		t.Fatal("CursorHidden after reset = true, want false")
	}
}

func TestCaptureSnapshotTrimsLiveScrollbackWidthMetadataWithCap(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(5, 1, 2)
	p := &Pane{
		ID:              1,
		emulator:        emu,
		scrollbackLines: 2,
		scrollbackLimit: 2,
	}
	wireScrollbackCallbacks(p)

	emu.Write([]byte("11111\r\n"))
	emu.Resize(6, 1)
	emu.Write([]byte("222222\r\n"))
	emu.Resize(7, 1)
	emu.Write([]byte("3333333\r\n"))

	snap := p.CaptureSnapshot()
	if len(snap.LiveHistory) != 2 {
		t.Fatalf("LiveHistory len = %d, want 2", len(snap.LiveHistory))
	}
	if got := snap.LiveHistory[0]; got.Text != "222222" || got.SourceWidth != 6 {
		t.Fatalf("LiveHistory[0] = %#v, want text=%q width=6", got, "222222")
	}
	if got := snap.LiveHistory[1]; got.Text != "3333333" || got.SourceWidth != 7 {
		t.Fatalf("LiveHistory[1] = %#v, want text=%q width=7", got, "3333333")
	}
}

func TestPaneScrollbackWidthClearsWithScrollback(t *testing.T) {
	t.Parallel()

	emu := NewVTEmulatorWithDrainAndScrollback(5, 1, 2)
	p := &Pane{
		ID:              1,
		emulator:        emu,
		scrollbackLines: 2,
		scrollbackLimit: 2,
	}
	wireScrollbackCallbacks(p)

	emu.Write([]byte("11111\r\n"))

	if got := p.ScrollbackSourceWidth(0); got != 5 {
		t.Fatalf("ScrollbackSourceWidth(0) = %d, want 5", got)
	}

	emu.(*vtEmulator).emu.ClearScrollback()

	if got := p.ScrollbackSourceWidth(0); got != 0 {
		t.Fatalf("ScrollbackSourceWidth(0) after clear = %d, want 0", got)
	}
}

func TestPaneRecordScrollbackPushKeepsAllocationsBounded(t *testing.T) {
	// Not parallel: testing.AllocsPerRun panics in parallel tests.
	p := &Pane{
		scrollbackLimit:  1024,
		scrollbackWidths: make([]int, 0, 1024),
	}

	if got := testing.AllocsPerRun(1000, func() {
		p.recordScrollbackPush(1, 80)
		p.clearScrollbackWidths()
	}); got != 0 {
		t.Fatalf("recordScrollbackPush allocated %.2f allocs/op, want 0", got)
	}
}
