package client

import "testing"

func TestPaneRenamePromptSubmitAndCancel(t *testing.T) {
	t.Parallel()

	t.Run("submit", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if !cr.ShowPaneRenamePrompt() {
			t.Fatal("ShowPaneRenamePrompt should succeed")
		}
		cr.HandlePaneRenamePromptInput([]byte("agent-1"))

		if overlay := cr.paneRenamePromptOverlay(); overlay == nil || overlay.Title != "rename-pane" || overlay.Input != "agent-1" {
			t.Fatalf("paneRenamePromptOverlay = %+v, want rename-pane input %q", overlay, "agent-1")
		}

		got := cr.HandlePaneRenamePromptInput([]byte{'\r'})
		if got.command != "rename" ||
			len(got.args) != 2 ||
			got.args[0] != "pane-1" ||
			got.args[1] != "agent-1" {
			t.Fatalf("enter action = %+v, want rename pane-1 agent-1", got)
		}
		if cr.PaneRenamePromptActive() {
			t.Fatal("PaneRenamePromptActive should be false after submit")
		}
	})

	t.Run("escape cancels", func(t *testing.T) {
		t.Parallel()

		cr := buildTestRenderer(t)
		if !cr.ShowPaneRenamePrompt() {
			t.Fatal("ShowPaneRenamePrompt should succeed")
		}
		cr.HandlePaneRenamePromptInput([]byte("agent-1"))

		got := cr.HandlePaneRenamePromptInput([]byte{0x1b})
		if got.bell || got.command != "" || len(got.args) != 0 {
			t.Fatalf("escape action = %+v, want cancel", got)
		}
		if cr.PaneRenamePromptActive() {
			t.Fatal("PaneRenamePromptActive should be false after escape")
		}
	})
}

func TestPaneRenamePromptRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{name: "empty"},
		{name: "slash", input: "agent/1"},
		{name: "whitespace", input: "agent 1"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cr := buildTestRenderer(t)
			if !cr.ShowPaneRenamePrompt() {
				t.Fatal("ShowPaneRenamePrompt should succeed")
			}
			if tt.input != "" {
				cr.HandlePaneRenamePromptInput([]byte(tt.input))
			}

			got := cr.HandlePaneRenamePromptInput([]byte{'\r'})
			if !got.bell || got.command != "" || len(got.args) != 0 {
				t.Fatalf("enter action = %+v, want validation bell", got)
			}
			if !cr.PaneRenamePromptActive() {
				t.Fatal("PaneRenamePromptActive should remain true after invalid submit")
			}
		})
	}
}

func TestPaneRenamePromptRequiresFocusedPane(t *testing.T) {
	t.Parallel()

	cr := NewClientRenderer(80, 24)
	if cr.ShowPaneRenamePrompt() {
		t.Fatal("ShowPaneRenamePrompt should fail without a layout")
	}

	cr = NewClientRenderer(80, 24)
	snap := twoPane80x23()
	snap.ActivePaneID = 99
	snap.Windows[0].ActivePaneID = 99
	cr.HandleLayout(snap)
	if cr.ShowPaneRenamePrompt() {
		t.Fatal("ShowPaneRenamePrompt should fail without a matching active pane")
	}
}
