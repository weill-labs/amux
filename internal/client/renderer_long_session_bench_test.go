package client

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func TestLongLivedSessionRendererBenchmarkWorkload(t *testing.T) {
	t.Parallel()

	workload := newLongLivedSessionRendererBench(t, longLivedSessionRendererBenchConfig{
		VisiblePanes: 4,
		HiddenPanes:  3,
		HistoryLines: 32,
		LineWidth:    80,
		LayoutHeight: 18,
	})
	defer workload.Close()

	if got := workload.VisiblePanes(); got != 4 {
		t.Fatalf("visible panes = %d, want 4", got)
	}
	if got := workload.HiddenPanes(); got != 3 {
		t.Fatalf("hidden panes = %d, want 3", got)
	}
	if got := workload.BaseHistoryLines(); got != 7*32 {
		t.Fatalf("base history lines = %d, want %d", got, 7*32)
	}

	dirty := workload.RenderDirtyFrame()
	if dirty.ANSIBytes == 0 {
		t.Fatal("dirty frame emitted no ANSI bytes")
	}
	if dirty.RenderStats.PanesComposited == 0 {
		t.Fatal("dirty frame composited no panes")
	}
	if dirty.VisibleOutputPanes == 0 {
		t.Fatal("dirty frame did not update visible panes")
	}
	if dirty.HiddenOutputPanes == 0 {
		t.Fatal("dirty frame did not update hidden panes")
	}
	if dirty.CaptureJSONBytes == 0 {
		t.Fatal("dirty frame did not build capture JSON with history")
	}

	full := workload.RenderFullFrame()
	if full.ANSIBytes == 0 {
		t.Fatal("full frame emitted no ANSI bytes")
	}
	if full.RenderStats.PanesComposited < dirty.RenderStats.PanesComposited {
		t.Fatalf("full frame panes composited = %d, want >= dirty frame %d", full.RenderStats.PanesComposited, dirty.RenderStats.PanesComposited)
	}
	if retained := workload.RetainedHeapAfterGC(); retained == 0 {
		t.Fatal("retained heap after GC should be observable")
	}
}

const (
	longLivedSessionBenchVisiblePanes = 12
	longLivedSessionBenchHiddenPanes  = 20
	longLivedSessionBenchHistoryLines = 512
	longLivedSessionBenchLineWidth    = 240
	longLivedSessionBenchLayoutHeight = 80
	longLivedSessionBenchScrollback   = 1024
)

type longLivedSessionRendererBenchConfig struct {
	VisiblePanes    int
	HiddenPanes     int
	HistoryLines    int
	LineWidth       int
	LayoutHeight    int
	ScrollbackLines int
}

func defaultLongLivedSessionRendererBenchConfig() longLivedSessionRendererBenchConfig {
	return longLivedSessionRendererBenchConfig{
		VisiblePanes:    longLivedSessionBenchVisiblePanes,
		HiddenPanes:     longLivedSessionBenchHiddenPanes,
		HistoryLines:    longLivedSessionBenchHistoryLines,
		LineWidth:       longLivedSessionBenchLineWidth,
		LayoutHeight:    longLivedSessionBenchLayoutHeight,
		ScrollbackLines: longLivedSessionBenchScrollback,
	}
}

func (cfg longLivedSessionRendererBenchConfig) normalize() longLivedSessionRendererBenchConfig {
	defaults := defaultLongLivedSessionRendererBenchConfig()
	if cfg.VisiblePanes <= 0 {
		cfg.VisiblePanes = defaults.VisiblePanes
	}
	if cfg.HiddenPanes < 0 {
		cfg.HiddenPanes = defaults.HiddenPanes
	}
	if cfg.HistoryLines <= 0 {
		cfg.HistoryLines = defaults.HistoryLines
	}
	if cfg.LineWidth <= 0 {
		cfg.LineWidth = defaults.LineWidth
	}
	if cfg.LayoutHeight <= render.GlobalBarHeight+mux.StatusLineRows {
		cfg.LayoutHeight = defaults.LayoutHeight
	}
	if cfg.ScrollbackLines <= 0 {
		cfg.ScrollbackLines = defaults.ScrollbackLines
	}
	return cfg
}

type longLivedSessionRendererBench struct {
	cr                 *ClientRenderer
	cfg                longLivedSessionRendererBenchConfig
	paneCells          map[uint32]proto.CellSnapshot
	visiblePaneIDs     []uint32
	hiddenPaneIDs      []uint32
	capturedHiddenPane uint32
	bufferedHiddenPane uint32
	agentStatus        map[uint32]proto.PaneAgentStatus
	step               int
	bytesPerFrame      int
}

type longLivedSessionFrameResult struct {
	VisibleOutputPanes int
	HiddenOutputPanes  int
	PaneOutputs        int
	ScreenChanges      int
	ANSIBytes          int
	CaptureJSONBytes   int
	InputBytes         int
	RenderStats        render.RenderStats
}

func newLongLivedSessionRendererBench(tb testing.TB, cfg longLivedSessionRendererBenchConfig) *longLivedSessionRendererBench {
	tb.Helper()

	cfg = cfg.normalize()
	layout, visiblePaneIDs, hiddenPaneIDs, paneCells := longLivedSessionLayoutSnapshot(cfg)
	stream := buildLongLivedSessionAttachReplay(tb, cfg, layout, paneCells)
	conn := &replayAttachConn{Reader: bytes.NewReader(stream)}
	cr := NewClientRendererWithScrollback(cfg.LineWidth, cfg.LayoutHeight+render.GlobalBarHeight, cfg.ScrollbackLines)
	if err := readAttachBootstrap(conn, proto.NewReader(conn), cr); err != nil {
		tb.Fatalf("read attach bootstrap: %v", err)
	}

	if data, _ := cr.renderDiff(); data == "" {
		tb.Fatal("initial long-lived session render emitted no ANSI bytes")
	}

	status := longLivedSessionAgentStatus(layout.Panes)
	var capturedHidden uint32
	var bufferedHidden uint32
	if len(hiddenPaneIDs) > 0 {
		capturedHidden = hiddenPaneIDs[0]
		if out := cr.CapturePaneJSON(capturedHidden, status); out == "" {
			tb.Fatalf("warm hidden pane capture for pane %d emitted no JSON", capturedHidden)
		}
		bufferedHidden = capturedHidden
		if len(hiddenPaneIDs) > 1 {
			bufferedHidden = hiddenPaneIDs[len(hiddenPaneIDs)-1]
		}
	}

	w := &longLivedSessionRendererBench{
		cr:                 cr,
		cfg:                cfg,
		paneCells:          paneCells,
		visiblePaneIDs:     visiblePaneIDs,
		hiddenPaneIDs:      hiddenPaneIDs,
		capturedHiddenPane: capturedHidden,
		bufferedHiddenPane: bufferedHidden,
		agentStatus:        status,
	}
	w.bytesPerFrame = w.computeBytesPerFrame()
	return w
}

func (w *longLivedSessionRendererBench) Close() {
	w.cr.renderer.Close()
}

func (w *longLivedSessionRendererBench) VisiblePanes() int {
	return len(w.visiblePaneIDs)
}

func (w *longLivedSessionRendererBench) HiddenPanes() int {
	return len(w.hiddenPaneIDs)
}

func (w *longLivedSessionRendererBench) BaseHistoryLines() int {
	total := 0
	for _, lines := range w.cr.loadState().baseHistory {
		total += len(lines)
	}
	return total
}

func (w *longLivedSessionRendererBench) RenderDirtyFrame() longLivedSessionFrameResult {
	return w.renderFrame(false)
}

func (w *longLivedSessionRendererBench) RenderFullFrame() longLivedSessionFrameResult {
	return w.renderFrame(true)
}

func (w *longLivedSessionRendererBench) RetainedHeapAfterGC() uint64 {
	runtime.GC()
	runtime.GC()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	runtime.KeepAlive(w)
	return mem.HeapAlloc
}

func (w *longLivedSessionRendererBench) renderFrame(fullRedraw bool) longLivedSessionFrameResult {
	result := longLivedSessionFrameResult{}
	outputPaneIDs := append([]uint32(nil), w.frameVisiblePaneIDs()...)
	outputPaneIDs = append(outputPaneIDs, w.frameHiddenPaneIDs()...)

	for _, paneID := range outputPaneIDs {
		payload := w.tinyDirtyPayload(paneID)
		result.InputBytes += len(payload)
		result.PaneOutputs++
		info := w.cr.handlePaneOutputRenderInfo(paneID, payload, 0)
		if info.paneVisible {
			result.VisibleOutputPanes++
		} else {
			result.HiddenOutputPanes++
		}
		if info.screenChanged {
			result.ScreenChanges++
		}
	}

	if fullRedraw {
		w.cr.RequestFullRedraw()
	}
	data, stats := w.cr.renderDiff()
	result.ANSIBytes = len(data)
	result.RenderStats = stats
	result.CaptureJSONBytes = len(w.cr.CaptureJSONWithHistory(w.agentStatus))
	w.step++
	return result
}

func (w *longLivedSessionRendererBench) frameVisiblePaneIDs() []uint32 {
	if len(w.visiblePaneIDs) <= 3 {
		return w.visiblePaneIDs
	}
	return []uint32{
		w.visiblePaneIDs[0],
		w.visiblePaneIDs[len(w.visiblePaneIDs)/2],
		w.visiblePaneIDs[len(w.visiblePaneIDs)-1],
	}
}

func (w *longLivedSessionRendererBench) frameHiddenPaneIDs() []uint32 {
	if len(w.hiddenPaneIDs) == 0 {
		return nil
	}
	ids := []uint32{w.capturedHiddenPane}
	if w.bufferedHiddenPane != 0 && w.bufferedHiddenPane != w.capturedHiddenPane {
		ids = append(ids, w.bufferedHiddenPane)
	}
	return ids
}

func (w *longLivedSessionRendererBench) tinyDirtyPayload(paneID uint32) []byte {
	cell := w.paneCells[paneID]
	height := mux.PaneContentHeight(cell.H)
	if height <= 0 {
		height = 1
	}
	width := cell.W
	if width <= 0 {
		width = w.cfg.LineWidth
	}
	row := (w.step+int(paneID))%height + 1
	maxCol := max(1, width-16)
	col := (w.step*3+int(paneID))%maxCol + 1
	return []byte(fmt.Sprintf("\x1b[%d;%dHp%02d:%06d", row, col, paneID, w.step))
}

func (w *longLivedSessionRendererBench) computeBytesPerFrame() int {
	total := 0
	for _, paneID := range append(w.frameVisiblePaneIDs(), w.frameHiddenPaneIDs()...) {
		total += len(w.tinyDirtyPayload(paneID))
	}
	return total
}

func longLivedSessionLayoutSnapshot(cfg longLivedSessionRendererBenchConfig) (*proto.LayoutSnapshot, []uint32, []uint32, map[uint32]proto.CellSnapshot) {
	visibleRoot, visiblePanes, visibleIDs := longLivedSessionWindowSnapshot(1, cfg.VisiblePanes, cfg.LineWidth, cfg.LayoutHeight)
	hiddenRoot, hiddenPanes, hiddenIDs := longLivedSessionWindowSnapshot(cfg.VisiblePanes+1, cfg.HiddenPanes, cfg.LineWidth, cfg.LayoutHeight)

	allPanes := make([]proto.PaneSnapshot, 0, len(visiblePanes)+len(hiddenPanes))
	allPanes = append(allPanes, visiblePanes...)
	allPanes = append(allPanes, hiddenPanes...)
	paneCells := make(map[uint32]proto.CellSnapshot, len(allPanes))
	longLivedSessionCollectCells(visibleRoot, paneCells)
	longLivedSessionCollectCells(hiddenRoot, paneCells)

	windows := []proto.WindowSnapshot{
		{
			ID:           1,
			Name:         "active-work",
			Index:        1,
			ActivePaneID: 1,
			Root:         visibleRoot,
			Panes:        visiblePanes,
		},
	}
	if cfg.HiddenPanes > 0 {
		windows = append(windows, proto.WindowSnapshot{
			ID:           2,
			Name:         "hidden-history",
			Index:        2,
			ActivePaneID: uint32(cfg.VisiblePanes + 1),
			Root:         hiddenRoot,
			Panes:        hiddenPanes,
		})
	}

	return &proto.LayoutSnapshot{
		SessionName:    "long-lived-bench",
		ActivePaneID:   1,
		Width:          cfg.LineWidth,
		Height:         cfg.LayoutHeight,
		Root:           visibleRoot,
		Panes:          allPanes,
		Windows:        windows,
		ActiveWindowID: 1,
	}, visibleIDs, hiddenIDs, paneCells
}

func longLivedSessionWindowSnapshot(startID, panes, width, height int) (proto.CellSnapshot, []proto.PaneSnapshot, []uint32) {
	if panes <= 0 {
		return proto.CellSnapshot{X: 0, Y: 0, W: width, H: height, Dir: -1}, nil, nil
	}

	rows := longLivedSessionGridRows(panes)
	cols := (panes + rows - 1) / rows
	root := proto.CellSnapshot{
		X:   0,
		Y:   0,
		W:   width,
		H:   height,
		Dir: int(mux.SplitHorizontal),
	}
	if rows == 1 {
		root.Dir = int(mux.SplitVertical)
	}

	paneSnaps := make([]proto.PaneSnapshot, 0, panes)
	paneIDs := make([]uint32, 0, panes)
	nextID := startID
	y := 0
	rowH := max(1, (height-(rows-1))/rows)
	for row := 0; row < rows && len(paneSnaps) < panes; row++ {
		h := rowH
		if row == rows-1 {
			h = height - y
		}
		rowPanes := min(cols, panes-len(paneSnaps))
		rowCell := proto.CellSnapshot{
			X:   0,
			Y:   y,
			W:   width,
			H:   h,
			Dir: int(mux.SplitVertical),
		}
		if rowPanes == 1 {
			rowCell.IsLeaf = true
			rowCell.Dir = -1
			rowCell.PaneID = uint32(nextID)
			root.Children = append(root.Children, rowCell)
			paneSnaps = append(paneSnaps, longLivedSessionPaneSnapshot(nextID, 0))
			paneIDs = append(paneIDs, uint32(nextID))
			nextID++
			y += h + 1
			continue
		}

		x := 0
		cellW := max(1, (width-(rowPanes-1))/rowPanes)
		for col := 0; col < rowPanes; col++ {
			w := cellW
			if col == rowPanes-1 {
				w = width - x
			}
			paneID := uint32(nextID)
			rowCell.Children = append(rowCell.Children, proto.CellSnapshot{
				X: x, Y: y, W: w, H: h,
				IsLeaf: true, Dir: -1, PaneID: paneID,
			})
			paneSnaps = append(paneSnaps, longLivedSessionPaneSnapshot(nextID, col))
			paneIDs = append(paneIDs, paneID)
			nextID++
			x += w + 1
		}
		root.Children = append(root.Children, rowCell)
		y += h + 1
	}

	if panes == 1 {
		root = root.Children[0]
	}
	return root, paneSnaps, paneIDs
}

func longLivedSessionGridRows(panes int) int {
	if panes <= 6 {
		return 1
	}
	return int(math.Ceil(float64(panes) / 6.0))
}

func longLivedSessionPaneSnapshot(id int, column int) proto.PaneSnapshot {
	paneID := uint32(id)
	return proto.PaneSnapshot{
		ID:          paneID,
		Name:        fmt.Sprintf("pane-%d", id),
		Host:        "local",
		Task:        "long-lived renderer benchmark",
		Color:       config.AccentColor(paneID - 1),
		ColumnIndex: column,
		Idle:        id%4 == 0,
		KV: map[string]string{
			"issue": "LAB-2015",
		},
	}
}

func longLivedSessionCollectCells(cell proto.CellSnapshot, cells map[uint32]proto.CellSnapshot) {
	if cell.IsLeaf {
		cells[cell.PaneID] = cell
		return
	}
	for _, child := range cell.Children {
		longLivedSessionCollectCells(child, cells)
	}
}

func buildLongLivedSessionAttachReplay(tb testing.TB, cfg longLivedSessionRendererBenchConfig, layout *proto.LayoutSnapshot, paneCells map[uint32]proto.CellSnapshot) []byte {
	tb.Helper()

	var buf bytes.Buffer
	writer := proto.NewWriter(&buf)
	writer.SetBinaryPaneHistory(true)
	if err := writer.WriteMsg(&proto.Message{Type: proto.MsgTypeLayout, Layout: layout}); err != nil {
		tb.Fatalf("write layout: %v", err)
	}
	for _, pane := range layout.Panes {
		history := longLivedSessionStyledHistory(pane.ID, cfg.HistoryLines, cfg.LineWidth)
		if err := writer.WriteMsg(&proto.Message{
			Type:          proto.MsgTypePaneHistory,
			PaneID:        pane.ID,
			History:       proto.StyledLineText(history),
			StyledHistory: history,
		}); err != nil {
			tb.Fatalf("write pane %d history: %v", pane.ID, err)
		}

		cell := paneCells[pane.ID]
		payload := longLivedSessionInitialScreen(pane.ID, cell.W, mux.PaneContentHeight(cell.H))
		if err := writer.WriteMsg(&proto.Message{
			Type:     proto.MsgTypePaneOutput,
			PaneID:   pane.ID,
			PaneData: payload,
		}); err != nil {
			tb.Fatalf("write pane %d output: %v", pane.ID, err)
		}
	}
	if err := writer.WriteMsg(&proto.Message{Type: proto.MsgTypeBell}); err != nil {
		tb.Fatalf("write bootstrap terminator: %v", err)
	}
	return buf.Bytes()
}

func longLivedSessionStyledHistory(paneID uint32, lines, width int) []proto.StyledLine {
	history := make([]proto.StyledLine, lines)
	cells := make([]proto.Cell, max(1, width))
	for i := range cells {
		cells[i] = proto.Cell{Char: "h", Width: 1}
	}
	lineWidth := max(1, width)
	for line := range history {
		prefix := fmt.Sprintf("pane-%02d attach history line %04d ", paneID, line)
		if len(prefix) > lineWidth {
			prefix = prefix[:lineWidth]
		}
		history[line] = proto.StyledLine{
			Text:  prefix + strings.Repeat("h", lineWidth-len(prefix)),
			Cells: cells,
		}
	}
	return history
}

func longLivedSessionInitialScreen(paneID uint32, width, height int) []byte {
	if height <= 0 {
		height = 1
	}
	lineWidth := max(1, width-1)
	var b strings.Builder
	for row := 0; row < height; row++ {
		prefix := fmt.Sprintf("pane-%02d bootstrap screen row %03d ", paneID, row)
		if len(prefix) > lineWidth {
			prefix = prefix[:lineWidth]
		}
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[3%dm%s%s\x1b[0m", row+1, (int(paneID)+row)%8, prefix, strings.Repeat("s", lineWidth-len(prefix)))
	}
	return []byte(b.String())
}

func longLivedSessionAgentStatus(panes []proto.PaneSnapshot) map[uint32]proto.PaneAgentStatus {
	status := make(map[uint32]proto.PaneAgentStatus, len(panes))
	for _, pane := range panes {
		status[pane.ID] = proto.PaneAgentStatus{
			Idle:           pane.ID%3 == 0,
			CurrentCommand: "codex",
		}
	}
	return status
}

func BenchmarkClientRendererLongLivedSession(b *testing.B) {
	cfg := defaultLongLivedSessionRendererBenchConfig()

	b.Run("dirty_frame", func(b *testing.B) {
		allocsPerFrame := measureLongLivedSessionAllocsPerFrame(b, cfg, false)
		workload := newLongLivedSessionRendererBench(b, cfg)
		defer workload.Close()

		b.SetBytes(int64(workload.bytesPerFrame))
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			result := workload.RenderDirtyFrame()
			if result.ANSIBytes == 0 {
				b.Fatal("dirty frame emitted no ANSI bytes")
			}
		}
		b.StopTimer()
		b.ReportMetric(allocsPerFrame, "allocs/frame")
		reportLongLivedSessionRetainedHeap(b, workload)
	})

	b.Run("full_frame", func(b *testing.B) {
		allocsPerFrame := measureLongLivedSessionAllocsPerFrame(b, cfg, true)
		workload := newLongLivedSessionRendererBench(b, cfg)
		defer workload.Close()

		b.SetBytes(int64(workload.bytesPerFrame))
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			result := workload.RenderFullFrame()
			if result.ANSIBytes == 0 {
				b.Fatal("full frame emitted no ANSI bytes")
			}
		}
		b.StopTimer()
		b.ReportMetric(allocsPerFrame, "allocs/frame")
		reportLongLivedSessionRetainedHeap(b, workload)
	})
}

func measureLongLivedSessionAllocsPerFrame(b *testing.B, cfg longLivedSessionRendererBenchConfig, fullRedraw bool) float64 {
	b.Helper()

	workload := newLongLivedSessionRendererBench(b, cfg)
	defer workload.Close()
	return testing.AllocsPerRun(3, func() {
		if fullRedraw {
			workload.RenderFullFrame()
		} else {
			workload.RenderDirtyFrame()
		}
	})
}

func reportLongLivedSessionRetainedHeap(b *testing.B, workload *longLivedSessionRendererBench) {
	b.Helper()

	b.ReportMetric(float64(workload.RetainedHeapAfterGC()), "retained-heap-B")
	b.ReportMetric(float64(workload.BaseHistoryLines()), "history-lines")
	b.ReportMetric(float64(workload.VisiblePanes()), "visible-panes")
	b.ReportMetric(float64(workload.HiddenPanes()), "hidden-panes")
}
