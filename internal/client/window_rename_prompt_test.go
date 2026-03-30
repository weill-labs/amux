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
