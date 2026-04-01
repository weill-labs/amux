package client

import (
	"strings"
	"testing"
)

func TestWindowRenamePromptDisplayOnly(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowWindowRenamePrompt() {
		t.Fatal("ShowWindowRenamePrompt should succeed")
	}
	if got := cr.HandleWindowRenamePromptInput([]byte("logs")); got.bell || got.command != "" {
		t.Fatalf("typing prompt input should edit locally, got %+v", got)
	}
	cr.RenderDiff()

	display := cr.CaptureDisplay()
	if !strings.Contains(display, "rename-window") || !strings.Contains(display, "> logs") {
		t.Fatalf("display capture should include rename prompt overlay, got:\n%s", display)
	}

	plain := cr.Capture(true)
	if strings.Contains(plain, "rename-window") || strings.Contains(plain, "> logs") {
		t.Fatalf("plain capture should not include rename prompt overlay, got:\n%s", plain)
	}
}

func TestWindowRenamePromptSubmitAndCancel(t *testing.T) {
	t.Parallel()

	t.Run("submit", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if !cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should succeed")
		}
		cr.HandleWindowRenamePromptInput([]byte("logs"))

		if overlay := cr.windowRenamePromptOverlay(); overlay == nil || overlay.Input != "logs" {
			t.Fatalf("windowRenamePromptOverlay = %+v, want input %q", overlay, "logs")
		}

		got := cr.HandleWindowRenamePromptInput([]byte{'\r'})
		if got.command != "rename-window" || len(got.args) != 1 || got.args[0] != "logs" {
			t.Fatalf("enter action = %+v, want rename-window logs", got)
		}
		if cr.WindowRenamePromptActive() {
			t.Fatal("WindowRenamePromptActive should be false after submit")
		}
	})

	t.Run("escape cancels", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if !cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should succeed")
		}
		cr.HandleWindowRenamePromptInput([]byte("logs"))

		got := cr.HandleWindowRenamePromptInput([]byte{0x1b})
		if got.bell || got.command != "" || len(got.args) != 0 {
			t.Fatalf("escape action = %+v, want cancel", got)
		}
		if cr.WindowRenamePromptActive() {
			t.Fatal("WindowRenamePromptActive should be false after escape")
		}
	})
}

func TestWindowRenamePromptEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("show prompt requires active window", func(t *testing.T) {
		t.Parallel()

		cr := NewClientRenderer(80, 24)
		if cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should fail without a layout")
		}

		cr = NewClientRenderer(80, 24)
		snap := twoPane80x23()
		snap.ActiveWindowID = 99
		cr.HandleLayout(snap)
		if cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should fail without a matching active window")
		}
	})

	t.Run("input helpers cover inactive and bell paths", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if got := cr.HandleWindowRenamePromptInput(nil); got.command != "" || len(got.args) != 0 || got.bell {
			t.Fatalf("HandleWindowRenamePromptInput(nil) = %+v, want zero", got)
		}
		if got := cr.HandleWindowRenamePromptInput([]byte("logs")); got.command != "" || len(got.args) != 0 || got.bell {
			t.Fatalf("HandleWindowRenamePromptInput without prompt = %+v, want zero", got)
		}
		if got := handleWindowRenamePromptInputOnRenderLoop(cr, nil, []byte("logs")); got.command != "" || len(got.args) != 0 || got.bell {
			t.Fatalf("handleWindowRenamePromptInputOnRenderLoop without prompt = %+v, want zero", got)
		}

		if !cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should succeed")
		}
		if got := cr.HandleWindowRenamePromptInput([]byte{0x1b, '[', 'A'}); !got.bell {
			t.Fatalf("arrow input should bell, got %+v", got)
		}
		if got := cr.HandleWindowRenamePromptInput([]byte{0xc3}); !got.bell {
			t.Fatalf("non-ascii input should bell, got %+v", got)
		}

		cr.editWindowRenamePrompt(-1, 0)
		if overlay := cr.windowRenamePromptOverlay(); overlay == nil || overlay.Input != "" {
			t.Fatalf("backspace on empty prompt = %+v, want empty input", overlay)
		}

		cr.HandleWindowRenamePromptInput([]byte("logs"))
		cr.HandleWindowRenamePromptInput([]byte{0x7f})
		if overlay := cr.windowRenamePromptOverlay(); overlay == nil || overlay.Input != "log" {
			t.Fatalf("backspace should remove one byte, got %+v", overlay)
		}
	})

	t.Run("submit handles nil and empty prompts", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if got := cr.submitWindowRenamePrompt(); got.command != "" || len(got.args) != 0 || got.bell {
			t.Fatalf("submitWindowRenamePrompt without prompt = %+v, want zero", got)
		}

		if !cr.ShowWindowRenamePrompt() {
			t.Fatal("ShowWindowRenamePrompt should succeed")
		}
		if got := cr.submitWindowRenamePrompt(); !got.bell {
			t.Fatalf("submitWindowRenamePrompt with empty input = %+v, want bell", got)
		}
	})

	t.Run("edit helper ignores missing prompt", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		cr.editWindowRenamePrompt(0, 'x')
		if cr.WindowRenamePromptActive() {
			t.Fatal("editWindowRenamePrompt should not create prompt state")
		}
	})
}

func TestWindowRenamePromptSupportsCursorEditingKeys(t *testing.T) {
	t.Parallel()

	cr := buildTestRenderer(t)
	if !cr.ShowWindowRenamePrompt() {
		t.Fatal("ShowWindowRenamePrompt should succeed")
	}

	cr.HandleWindowRenamePromptInput([]byte("logs"))
	if got := cr.HandleWindowRenamePromptInput([]byte("\x1b[D")); got.bell {
		t.Fatalf("left arrow should move the cursor, got %+v", got)
	}
	if got := cr.HandleWindowRenamePromptInput([]byte("\x1b[D")); got.bell {
		t.Fatalf("second left arrow should move the cursor, got %+v", got)
	}
	if got := cr.HandleWindowRenamePromptInput([]byte{0x0b}); got.bell {
		t.Fatalf("ctrl-k should delete to end, got %+v", got)
	}
	if overlay := cr.windowRenamePromptOverlay(); overlay == nil || overlay.Input != "lo" {
		t.Fatalf("prompt after ctrl-k = %+v, want input %q", overlay, "lo")
	}

	if got := cr.HandleWindowRenamePromptInput([]byte{0x01}); got.bell {
		t.Fatalf("ctrl-a should move to start, got %+v", got)
	}
	if got := cr.HandleWindowRenamePromptInput([]byte("x")); got.bell {
		t.Fatalf("typing after ctrl-a should edit locally, got %+v", got)
	}

	submit := cr.HandleWindowRenamePromptInput([]byte{'\r'})
	if submit.command != "rename-window" || len(submit.args) != 1 || submit.args[0] != "xlo" {
		t.Fatalf("submit after cursor edits = %+v, want rename-window xlo", submit)
	}
}
