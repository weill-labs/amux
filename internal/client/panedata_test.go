package client

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
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

	pane := &PaneData{Emu: emu}
	if got := pane.RenderScreen(true); !strings.Contains(got, "\033[7m") {
		t.Fatal("active RenderScreen should preserve reverse-video cursor block")
	}
	if got := pane.RenderScreen(false); strings.Contains(got, "\033[7m") {
		t.Fatal("inactive RenderScreen should strip isolated reverse-video cursor block")
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

		pane := &PaneData{Emu: emu}
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

		pane := &PaneData{Emu: emu}
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

		pane := &PaneData{Emu: emu}
		cell := pane.CellAt(6, 0, false)
		if cell.Style.Attrs&uv.AttrReverse == 0 {
			t.Fatal("inactive CellAt should preserve off-cursor reverse-video space")
		}
	})
}

func TestPaneDataAccessors(t *testing.T) {
	t.Parallel()

	emu := newTestVTEmulator(20, 4)
	pane := &PaneData{
		Emu: emu,
		Info: proto.PaneSnapshot{
			ID:         7,
			Name:       "pane-7",
			Host:       "buildbox",
			Task:       "tail -f",
			Color:      "89dceb",
			Minimized:  true,
			Idle:       true,
			ConnStatus: "connected",
		},
	}

	if got := pane.ID(); got != 7 {
		t.Fatalf("ID() = %d, want 7", got)
	}
	if got := pane.Name(); got != "pane-7" {
		t.Fatalf("Name() = %q, want pane-7", got)
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
	if !pane.Minimized() {
		t.Fatal("Minimized() = false, want true")
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
