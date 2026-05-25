package client

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

const (
	busyMultiPaneBenchWidth        = 240
	busyMultiPaneBenchHeight       = 82
	busyMultiPaneBenchLayoutHeight = busyMultiPaneBenchHeight - render.GlobalBarHeight
	busyMultiPaneBenchRows         = 2
	busyMultiPaneBenchCols         = 5
	busyMultiPaneBenchPanes        = busyMultiPaneBenchRows * busyMultiPaneBenchCols
)

type busyMultiPaneClientRenderBench struct {
	cr             *ClientRenderer
	paneCells      map[uint32]proto.CellSnapshot
	outputPaneIDs  []uint32
	step           int
	bytesPerStep   int
	visiblePaneIDs []uint32
}

type busyMultiPaneClientRenderResult struct {
	VisiblePanes         int
	PaneOutputs          int
	ScreenChangedOutputs int
	ANSIBytes            int
	InputBytes           int
	RenderStats          render.RenderStats
}

func TestBusyMultiPaneClientRendererBenchmarkWorkload(t *testing.T) {
	t.Parallel()

	workload := newBusyMultiPaneClientRenderBench(t)
	defer workload.Close()

	result := workload.Step()
	if result.VisiblePanes != 10 {
		t.Fatalf("visible panes = %d, want 10", result.VisiblePanes)
	}
	if result.PaneOutputs != 4 {
		t.Fatalf("pane outputs = %d, want 4", result.PaneOutputs)
	}
	if result.ScreenChangedOutputs != result.PaneOutputs {
		t.Fatalf("screen changed outputs = %d, want %d", result.ScreenChangedOutputs, result.PaneOutputs)
	}
	if result.RenderStats.PanesComposited == 0 {
		t.Fatal("render composed no panes")
	}
	if result.ANSIBytes == 0 {
		t.Fatal("render emitted no ANSI bytes")
	}
}

func BenchmarkClientRendererBusyMultiPaneRenderLoop(b *testing.B) {
	workload := newBusyMultiPaneClientRenderBench(b)
	defer workload.Close()

	b.SetBytes(int64(workload.bytesPerStep))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result := workload.Step()
		if result.ANSIBytes == 0 {
			b.Fatal("render emitted no ANSI bytes")
		}
	}
}

func newBusyMultiPaneClientRenderBench(tb testing.TB) *busyMultiPaneClientRenderBench {
	tb.Helper()

	layout := busyMultiPaneBenchLayoutSnapshot()
	paneCells := busyMultiPaneBenchPaneCells(layout.Root)
	cr := NewClientRendererWithScrollback(busyMultiPaneBenchWidth, busyMultiPaneBenchHeight, mux.DefaultScrollbackLines)
	cr.HandleLayout(layout)

	for _, paneID := range busyMultiPaneBenchPaneIDs() {
		cell := paneCells[paneID]
		payload := busyMultiPaneInitialScreen(paneID, cell.W, mux.PaneContentHeight(cell.H))
		if !cr.HandlePaneOutput(paneID, payload) {
			tb.Fatalf("preload pane %d did not affect visible frame", paneID)
		}
	}
	if data, _ := cr.renderDiff(); data == "" {
		tb.Fatal("initial render emitted no ANSI bytes")
	}

	outputPaneIDs := []uint32{1, 3, 6, 9}
	bytesPerStep := 0
	for i, paneID := range outputPaneIDs {
		cell := paneCells[paneID]
		bytesPerStep += len(busyMultiPaneTickPayload(paneID, cell.W, mux.PaneContentHeight(cell.H), i))
	}

	return &busyMultiPaneClientRenderBench{
		cr:             cr,
		paneCells:      paneCells,
		outputPaneIDs:  outputPaneIDs,
		bytesPerStep:   bytesPerStep,
		visiblePaneIDs: busyMultiPaneBenchPaneIDs(),
	}
}

func (w *busyMultiPaneClientRenderBench) Close() {
	w.cr.renderer.Close()
}

func (w *busyMultiPaneClientRenderBench) Step() busyMultiPaneClientRenderResult {
	screenChanged := 0
	inputBytes := 0
	for i, paneID := range w.outputPaneIDs {
		cell := w.paneCells[paneID]
		payload := busyMultiPaneTickPayload(paneID, cell.W, mux.PaneContentHeight(cell.H), w.step+i)
		inputBytes += len(payload)
		info := w.cr.handlePaneOutputRenderInfo(paneID, payload, 0)
		if info.screenChanged {
			screenChanged++
		}
	}
	data, stats := w.cr.renderDiff()
	w.step++
	return busyMultiPaneClientRenderResult{
		VisiblePanes:         len(w.visiblePaneIDs),
		PaneOutputs:          len(w.outputPaneIDs),
		ScreenChangedOutputs: screenChanged,
		ANSIBytes:            len(data),
		InputBytes:           inputBytes,
		RenderStats:          stats,
	}
}

func busyMultiPaneBenchLayoutSnapshot() *proto.LayoutSnapshot {
	root := proto.CellSnapshot{
		X: 0, Y: 0, W: busyMultiPaneBenchWidth, H: busyMultiPaneBenchLayoutHeight,
		Dir: int(mux.SplitHorizontal),
	}

	panes := make([]proto.PaneSnapshot, 0, busyMultiPaneBenchPanes)
	paneID := uint32(1)
	y := 0
	rowH := (busyMultiPaneBenchLayoutHeight - (busyMultiPaneBenchRows - 1)) / busyMultiPaneBenchRows
	for row := 0; row < busyMultiPaneBenchRows; row++ {
		h := rowH
		if row == busyMultiPaneBenchRows-1 {
			h = busyMultiPaneBenchLayoutHeight - y
		}
		rowCell := proto.CellSnapshot{
			X: 0, Y: y, W: busyMultiPaneBenchWidth, H: h,
			Dir: int(mux.SplitVertical),
		}
		x := 0
		cellW := (busyMultiPaneBenchWidth - (busyMultiPaneBenchCols - 1)) / busyMultiPaneBenchCols
		for col := 0; col < busyMultiPaneBenchCols; col++ {
			w := cellW
			if col == busyMultiPaneBenchCols-1 {
				w = busyMultiPaneBenchWidth - x
			}
			rowCell.Children = append(rowCell.Children, proto.CellSnapshot{
				X: x, Y: y, W: w, H: h,
				IsLeaf: true, Dir: -1, PaneID: paneID,
			})
			panes = append(panes, proto.PaneSnapshot{
				ID:          paneID,
				Name:        fmt.Sprintf("pane-%d", paneID),
				Host:        "local",
				Task:        "busy renderer benchmark",
				Color:       config.AccentColor(paneID - 1),
				ColumnIndex: col,
				Idle:        false,
			})
			x += w + 1
			paneID++
		}
		root.Children = append(root.Children, rowCell)
		y += h + 1
	}

	window := proto.WindowSnapshot{
		ID:           1,
		Name:         "bench",
		Index:        1,
		ActivePaneID: 1,
		Root:         root,
		Panes:        panes,
	}
	return &proto.LayoutSnapshot{
		SessionName:    "bench",
		ActivePaneID:   1,
		Width:          busyMultiPaneBenchWidth,
		Height:         busyMultiPaneBenchLayoutHeight,
		Root:           root,
		Panes:          panes,
		Windows:        []proto.WindowSnapshot{window},
		ActiveWindowID: 1,
	}
}

func busyMultiPaneBenchPaneIDs() []uint32 {
	ids := make([]uint32, busyMultiPaneBenchPanes)
	for i := range ids {
		ids[i] = uint32(i + 1)
	}
	return ids
}

func busyMultiPaneBenchPaneCells(root proto.CellSnapshot) map[uint32]proto.CellSnapshot {
	cells := make(map[uint32]proto.CellSnapshot, busyMultiPaneBenchPanes)
	var walk func(proto.CellSnapshot)
	walk = func(cell proto.CellSnapshot) {
		if cell.IsLeaf {
			cells[cell.PaneID] = cell
			return
		}
		for _, child := range cell.Children {
			walk(child)
		}
	}
	walk(root)
	return cells
}

func busyMultiPaneInitialScreen(paneID uint32, width, height int) []byte {
	var b strings.Builder
	lineWidth := max(1, width-1)
	for row := 0; row < height; row++ {
		text := busyMultiPaneLine(fmt.Sprintf("pane-%02d warm row %02d ", paneID, row), lineWidth, string(rune('a'+row%26)))
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[1;3%d;4%dm%s\x1b[0m", row+1, (int(paneID)+row)%8, (int(paneID)+row+1)%8, text)
	}
	return []byte(b.String())
}

func busyMultiPaneTickPayload(paneID uint32, width, height, seq int) []byte {
	row := (seq*7 + int(paneID)) % max(1, height)
	lineWidth := max(1, width-1)
	text := busyMultiPaneLine(fmt.Sprintf("pane-%02d tick %08d row %02d ", paneID, seq, row), lineWidth, string(rune('A'+seq%26)))
	return []byte(fmt.Sprintf("\x1b[%d;1H\x1b[%d;3%d;4%dm%s\x1b[0m", row+1, 1+seq%2, (int(paneID)+seq)%8, (int(paneID)+seq+3)%8, text))
}

func busyMultiPaneLine(prefix string, width int, fill string) string {
	if len(prefix) > width {
		return prefix[:width]
	}
	return prefix + strings.Repeat(fill, width-len(prefix))
}
