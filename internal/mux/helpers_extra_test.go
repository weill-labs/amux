package mux

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mouse"
)

func TestLayoutCellIDHelpers(t *testing.T) {
	t.Parallel()

	leafByID := NewLeafByID(7, 1, 2, 10, 4)
	if got := leafByID.CellPaneID(); got != 7 {
		t.Fatalf("CellPaneID() = %d, want 7", got)
	}
	if leafByID.Pane != nil || !leafByID.IsLeaf() {
		t.Fatalf("NewLeafByID() = %+v, want client-side leaf", leafByID)
	}

	leafWithPane := NewLeaf(&Pane{ID: 9}, 0, 0, 8, 4)
	root := &LayoutCell{
		Dir:      SplitVertical,
		Children: []*LayoutCell{leafByID, leafWithPane},
	}
	leafByID.Parent = root
	leafWithPane.Parent = root

	if got := root.CellPaneID(); got != 0 {
		t.Fatalf("internal CellPaneID() = %d, want 0", got)
	}
	if got := root.FindByPaneID(7); got != leafByID {
		t.Fatalf("FindByPaneID(7) = %+v, want leafByID", got)
	}
	if got := root.FindByPaneID(9); got != leafWithPane {
		t.Fatalf("FindByPaneID(9) = %+v, want leafWithPane", got)
	}
	if got := root.FindByPaneID(99); got != nil {
		t.Fatalf("FindByPaneID(99) = %+v, want nil", got)
	}
	if got := leafByID.IndexInParent(); got != 0 {
		t.Fatalf("indexInParent(first child) = %d, want 0", got)
	}
	if got := leafWithPane.IndexInParent(); got != 1 {
		t.Fatalf("indexInParent(second child) = %d, want 1", got)
	}
	if got := (&LayoutCell{}).IndexInParent(); got != -1 {
		t.Fatalf("indexInParent(rootless) = %d, want -1", got)
	}
}

func TestPaneEnvironmentAndCreatedAt(t *testing.T) {
	base := []string{
		"TERM=screen-256color",
		"AMUX_PANE=old",
		"AMUX_SESSION=old-session",
		"NO_COLOR=1",
		"CODEX_CI=1",
		"PATH=/bin",
		"ODDENTRY",
	}

	env := paneCommandEnv(base, 7, "session-a")
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"TERM=screen-256color", "AMUX_PANE=old", "AMUX_SESSION=old-session", "NO_COLOR=1", "CODEX_CI=1"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("paneCommandEnv leaked %q:\n%s", forbidden, joined)
		}
	}
	for _, required := range []string{"TERM=amux", "AMUX_PANE=7", "AMUX_SESSION=session-a", "PATH=/bin", "ODDENTRY"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("paneCommandEnv missing %q:\n%s", required, joined)
		}
	}

	t.Setenv("TERM", "xterm-256color")
	t.Setenv("AMUX_PANE", "old-pane")
	t.Setenv("AMUX_SESSION", "old-session")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CODEX_CI", "1")
	shellEnv := strings.Join(paneShellEnv(8, "session-b"), "\n")
	for _, required := range []string{"TERM=amux", "AMUX_PANE=8", "AMUX_SESSION=session-b"} {
		if !strings.Contains(shellEnv, required) {
			t.Fatalf("paneShellEnv missing %q:\n%s", required, shellEnv)
		}
	}
	for _, forbidden := range []string{"NO_COLOR=1", "CODEX_CI=1", "AMUX_PANE=old-pane", "AMUX_SESSION=old-session"} {
		if strings.Contains(shellEnv, forbidden) {
			t.Fatalf("paneShellEnv leaked %q:\n%s", forbidden, shellEnv)
		}
	}

	pane := &Pane{}
	want := time.Unix(1234, 5678)
	pane.SetCreatedAt(want)
	if got := pane.CreatedAt(); !got.Equal(want) {
		t.Fatalf("CreatedAt() = %v, want %v", got, want)
	}
}

func TestEncodeMouseButton(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		btn  mouse.Button
		want ansi.MouseButton
		ok   bool
	}{
		{name: "left", btn: mouse.ButtonLeft, want: ansi.MouseLeft, ok: true},
		{name: "middle", btn: mouse.ButtonMiddle, want: ansi.MouseMiddle, ok: true},
		{name: "right", btn: mouse.ButtonRight, want: ansi.MouseRight, ok: true},
		{name: "none", btn: mouse.ButtonNone, want: ansi.MouseNone, ok: true},
		{name: "scroll up", btn: mouse.ScrollUp, want: ansi.MouseWheelUp, ok: true},
		{name: "scroll down", btn: mouse.ScrollDown, want: ansi.MouseWheelDown, ok: true},
		{name: "scroll left", btn: mouse.ScrollLeft, want: ansi.MouseWheelLeft, ok: true},
		{name: "scroll right", btn: mouse.ScrollRight, want: ansi.MouseWheelRight, ok: true},
		{name: "invalid", btn: mouse.Button(99), ok: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := encodeMouseButton(tt.btn)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("encodeMouseButton(%v) = (%v, %t), want (%v, %t)", tt.btn, got, ok, tt.want, tt.ok)
			}
		})
	}
}
