package client

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClientRendererTrimsStyledAttachHistoryAfterLiveScrollbackRetainsLimit(t *testing.T) {
	t.Parallel()

	const scrollbackLines = 3
	cr := NewClientRendererWithScrollback(80, 24, scrollbackLines)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(twoPane80x23())

	for _, paneID := range []uint32{1, 2} {
		cr.HandlePaneHistoryStyled(paneID, styledHistoryRetentionLines(paneID, scrollbackLines))
		cr.HandlePaneOutput(paneID, liveHistoryRetentionOutput(paneID, 30))
	}

	for _, paneID := range []uint32{1, 2} {
		if got := len(cr.loadState().baseHistory[paneID]); got != 0 {
			t.Fatalf("pane-%d retained base history len = %d, want 0 after live scrollback reaches limit", paneID, got)
		}
	}

	var capture proto.CaptureJSON
	out := cr.CaptureJSONWithHistory(nil)
	if err := json.Unmarshal([]byte(out), &capture); err != nil {
		t.Fatalf("JSON parse: %v\nraw: %s", err, out)
	}
	for _, pane := range capture.Panes {
		for _, line := range pane.Content {
			if strings.Contains(line, "-old-") {
				t.Fatalf("pane-%d capture retained attach history line %q after live scrollback reached limit", pane.ID, line)
			}
		}
	}

	cr.EnterCopyMode(1)
	cm := cr.CopyModeForPane(1)
	if cm == nil {
		t.Fatal("copy mode should exist for pane-1")
	}
	for i := 0; i < cm.TotalLines(); i++ {
		if line := cm.LineText(i); strings.Contains(line, "-old-") {
			t.Fatalf("copy mode retained attach history line %q after live scrollback reached limit", line)
		}
	}
}

func TestClientRendererTrimsStyledAttachHistoryAfterBatchedPaneOutput(t *testing.T) {
	t.Parallel()

	const scrollbackLines = 3
	cr := NewClientRendererWithScrollback(80, 24, scrollbackLines)
	t.Cleanup(cr.renderer.Close)
	cr.HandleLayout(twoPane80x23())

	for _, paneID := range []uint32{1, 2} {
		cr.HandlePaneHistoryStyled(paneID, styledHistoryRetentionLines(paneID, scrollbackLines))
	}
	cr.handlePaneOutputBatch([]*RenderMsg{
		{Typ: RenderMsgPaneOutput, PaneID: 1, Data: liveHistoryRetentionOutput(1, 30)},
		{Typ: RenderMsgPaneOutput, PaneID: 2, Data: liveHistoryRetentionOutput(2, 30)},
	})

	for _, paneID := range []uint32{1, 2} {
		if got := len(cr.loadState().baseHistory[paneID]); got != 0 {
			t.Fatalf("pane-%d retained base history len = %d, want 0 after batched output reaches live scrollback limit", paneID, got)
		}
	}
}

func TestSetTrimmedBaseHistoryKeepsStyledTail(t *testing.T) {
	t.Parallel()

	history := styledHistoryRetentionLines(1, 4)
	histories := map[uint32][]proto.StyledLine{1: history}

	setTrimmedBaseHistory(histories, 1, 2, 3)

	got := histories[1]
	if len(got) != 1 {
		t.Fatalf("trimmed history len = %d, want 1", len(got))
	}
	if got[0].Text != "pane-1-old-03" {
		t.Fatalf("trimmed history text = %q, want newest line", got[0].Text)
	}
	if len(got[0].Cells) == 0 || got[0].Cells[0].Style.Attrs&uv.AttrBold == 0 {
		t.Fatal("trimmed history should preserve styled cells for kept lines")
	}

	history[3].Text = "mutated"
	if got[0].Text != "pane-1-old-03" {
		t.Fatalf("trimmed history shares caller line headers, got %q", got[0].Text)
	}
}

func styledHistoryRetentionLines(paneID uint32, lines int) []proto.StyledLine {
	history := make([]proto.StyledLine, lines)
	for i := range history {
		text := fmt.Sprintf("pane-%d-old-%02d", paneID, i)
		cells := make([]proto.Cell, 0, len(text))
		for _, r := range text {
			cells = append(cells, proto.Cell{
				Char:  string(r),
				Width: 1,
				Style: uv.Style{Attrs: uv.AttrBold},
			})
		}
		history[i] = proto.StyledLine{Text: text, Cells: cells}
	}
	return history
}

func liveHistoryRetentionOutput(paneID uint32, lines int) []byte {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "pane-%d-live-%02d\r\n", paneID, i)
	}
	return []byte(b.String())
}
