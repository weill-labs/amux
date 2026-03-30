package client

import (
	"reflect"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func newTestVTEmulator(width, height int) mux.TerminalEmulator {
	return mux.NewVTEmulatorWithScrollback(width, height, mux.DefaultScrollbackLines)
}

func TestPaneDataRenderScreen(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(20, 4)
	if _, err := emu.Write([]byte("hello \033[7m \033[m\033[1D")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	pane := &clientPaneData{emu: emu}
	if got := pane.RenderScreen(true); !strings.Contains(got, "\033[7m") {
		t.Fatal("active RenderScreen should preserve reverse-video cursor block")
	}
	if got := pane.RenderScreen(false); strings.Contains(got, "\033[7m") {
		t.Fatal("inactive RenderScreen should strip isolated reverse-video cursor block")
	}
}

func TestPaneDataRenderScreenStripsOSC8HyperlinksWhenUnsupported(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(40, 4)
	if _, err := emu.Write([]byte("\033]8;;https://example.com\033\\test-link\033]8;;\033\\")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	pane := &clientPaneData{emu: emu}
	got := pane.RenderScreen(true)
	if strings.Contains(got, "\033]8;") {
		t.Fatalf("RenderScreen should strip OSC 8 when hyperlinks are unsupported, got %q", got)
	}
	if !strings.Contains(got, "test-link") {
		t.Fatalf("RenderScreen should preserve visible link text, got %q", got)
	}
}

func TestPaneDataRenderScreenPreservesOSC8HyperlinksWhenSupported(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(40, 4)
	if _, err := emu.Write([]byte("\033]8;;https://example.com\033\\test-link\033]8;;\033\\")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	pane := &clientPaneData{
		emu:  emu,
		caps: proto.ClientCapabilities{Hyperlinks: true},
	}
	got := pane.RenderScreen(true)
	if !strings.Contains(got, "\033]8;") {
		t.Fatalf("RenderScreen should preserve OSC 8 when hyperlinks are supported, got %q", got)
	}
}

func TestPaneDataCellAt(t *testing.T) {
	t.Parallel()

	t.Run("inactive strips isolated cursor block", func(t *testing.T) {
		t.Parallel()

		emu := newTestVTEmulator(20, 4)
		if _, err := emu.Write([]byte("hello \033[7m \033[m\033[1D")); err != nil {
			t.Fatalf("Write: %v", err)
		}

		pane := &clientPaneData{emu: emu}
		active := pane.CellAt(6, 0, true)
		if active.Style.Attrs&uv.AttrReverse == 0 {
			t.Fatal("active CellAt should keep reverse-video attribute")
		}

		inactive := pane.CellAt(6, 0, false)
		if inactive.Style.Attrs&uv.AttrReverse != 0 {
			t.Fatal("inactive CellAt should clear reverse-video attribute for cursor block")
		}
	})

	t.Run("inactive preserves multi-cell reverse video", func(t *testing.T) {
		t.Parallel()

		emu := newTestVTEmulator(20, 4)
		if _, err := emu.Write([]byte("\033[7mselected\033[m")); err != nil {
			t.Fatalf("Write: %v", err)
		}

		pane := &clientPaneData{emu: emu}
		cell := pane.CellAt(1, 0, false)
		if cell.Style.Attrs&uv.AttrReverse == 0 {
			t.Fatal("inactive CellAt should preserve reverse-video for non-cursor highlights")
		}
	})

	t.Run("inactive preserves isolated reverse video away from cursor", func(t *testing.T) {
		t.Parallel()

		emu := newTestVTEmulator(20, 4)
		if _, err := emu.Write([]byte("hello \033[7m \033[m")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if _, err := emu.Write([]byte("\033[1;1H")); err != nil {
			t.Fatalf("Write cursor move: %v", err)
		}

		pane := &clientPaneData{emu: emu}
		cell := pane.CellAt(6, 0, false)
		if cell.Style.Attrs&uv.AttrReverse == 0 {
			t.Fatal("inactive CellAt should preserve off-cursor reverse-video space")
		}
	})
}

func TestPaneDataAccessors(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(20, 4)
	pane := &clientPaneData{
		emu: emu,
		info: proto.PaneSnapshot{
			ID:            7,
			Name:          "pane-7",
			TrackedPRs:    []proto.TrackedPR{{Number: 42}, {Number: 314}},
			TrackedIssues: []proto.TrackedIssue{{ID: "LAB-339"}},
			Host:          "buildbox",
			Task:          "tail -f",
			Color:         "89dceb",
			Idle:          true,
			ConnStatus:    "connected",
		},
	}

	if got := pane.ID(); got != 7 {
		t.Fatalf("ID() = %d, want 7", got)
	}
	if got := pane.Name(); got != "pane-7" {
		t.Fatalf("Name() = %q, want pane-7", got)
	}
	if got := proto.TrackedPRNumbers(pane.TrackedPRs()); !reflect.DeepEqual(got, []int{42, 314}) {
		t.Fatalf("TrackedPRs() = %v, want [42 314]", got)
	}
	if got := proto.TrackedIssueIDs(pane.TrackedIssues()); !reflect.DeepEqual(got, []string{"LAB-339"}) {
		t.Fatalf("TrackedIssues() = %v, want [LAB-339]", got)
	}
	if got := pane.Host(); got != "buildbox" {
		t.Fatalf("Host() = %q, want buildbox", got)
	}
	if got := pane.Task(); got != "tail -f" {
		t.Fatalf("Task() = %q, want tail -f", got)
	}
	if got := pane.Color(); got != "89dceb" {
		t.Fatalf("Color() = %q, want 89dceb", got)
	}
	if !pane.Idle() {
		t.Fatal("Idle() = false, want true")
	}
	if got := pane.ConnStatus(); got != "connected" {
		t.Fatalf("ConnStatus() = %q, want connected", got)
	}
	if pane.InCopyMode() {
		t.Fatal("InCopyMode() = true, want false")
	}
	if got := pane.CopyModeSearch(); got != "" {
		t.Fatalf("CopyModeSearch() = %q, want empty", got)
	}
}

func TestPaneDataCopyModeUsesFrozenBufferAndExposesOverlay(t *testing.T) {
	t.Parallel()

	copyBuffer := paneBufferSnapshot{
		width:  5,
		height: 1,
		screen: []paneBufferLine{{
			text: "hello",
			cells: []render.ScreenCell{
				{Char: "h", Width: 1},
				{Char: "e", Width: 1},
				{Char: "l", Width: 1},
				{Char: "l", Width: 1},
				{Char: "o", Width: 1},
			},
		}},
	}
	liveEmu := newTestVTEmulator(5, 1)
	if _, err := liveEmu.Write([]byte("world")); err != nil {
		t.Fatalf("Write live emulator: %v", err)
	}

	cm := copymode.New(copyBuffer, 5, 1, 0)
	if action := cm.StartSelection(); action != copymode.ActionRedraw {
		t.Fatalf("StartSelection() = %d, want %d", action, copymode.ActionRedraw)
	}
	cm.HandleInput([]byte{'l'})

	pane := &clientPaneData{emu: liveEmu, cm: cm}
	if got := pane.CellAt(1, 0, true); got.Char != "e" {
		t.Fatalf("copy-mode CellAt(1, 0) = %q, want frozen buffer char %q", got.Char, "e")
	}
	if pane.CellAt(1, 0, true).Style.Bg != nil {
		t.Fatal("copy-mode CellAt should return the base frozen cell without overlay styling")
	}

	overlay := pane.CopyModeOverlay()
	if overlay == nil {
		t.Fatal("CopyModeOverlay() = nil, want overlay")
	}
	if overlay.Selection == nil {
		t.Fatal("overlay.Selection = nil, want selection range")
	}
}
