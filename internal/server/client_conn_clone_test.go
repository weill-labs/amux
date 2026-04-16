package server

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/proto"
)

func TestCloneMessageDeepCopiesPaneHistorySlices(t *testing.T) {
	t.Parallel()

	original := &Message{
		Type:    MsgTypePaneHistory,
		PaneID:  9,
		History: []string{"plain"},
		StyledHistory: []proto.StyledLine{
			{
				Text: "styled",
				Cells: []proto.Cell{
					{Char: "x", Width: 1, Style: uv.Style{Fg: ansi.BasicColor(2)}},
				},
			},
		},
	}

	cloned := cloneMessage(original)
	if cloned == nil {
		t.Fatal("cloneMessage() = nil, want clone")
	}
	if len(cloned.History) != 1 || len(cloned.StyledHistory) != 1 || len(cloned.StyledHistory[0].Cells) != 1 {
		t.Fatalf("cloneMessage() = %#v, want populated deep copy", cloned)
	}

	cloned.History[0] = "mutated plain"
	cloned.StyledHistory[0].Text = "mutated styled"
	cloned.StyledHistory[0].Cells[0].Char = "y"

	if original.History[0] != "plain" {
		t.Fatalf("original history = %q, want plain", original.History[0])
	}
	if original.StyledHistory[0].Text != "styled" {
		t.Fatalf("original styled text = %q, want styled", original.StyledHistory[0].Text)
	}
	if original.StyledHistory[0].Cells[0].Char != "x" {
		t.Fatalf("original styled char = %q, want x", original.StyledHistory[0].Cells[0].Char)
	}
}
