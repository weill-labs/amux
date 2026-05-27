package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

// Benchmarks reuse fakePaneData from compositor_test.go (same package).

// benchLayoutTree builds a layout tree with n panes fitting in w×h,
// returning the root and the list of pane IDs.
func benchLayoutTree(n, w, h int) (*mux.LayoutCell, []uint32) {
	ids := make([]uint32, 0, n)
	root := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, w, h)
	ids = append(ids, 1)
	for i := 2; i <= n; i++ {
		dir := mux.SplitVertical
		if i%2 == 0 {
			dir = mux.SplitHorizontal
		}

		var target *mux.LayoutCell
		bestSize := -1
		root.Walk(func(c *mux.LayoutCell) {
			size := c.W
			if dir == mux.SplitHorizontal {
				size = c.H
			}
			if size >= 2*mux.PaneMinSize+1 && size > bestSize {
				target = c
				bestSize = size
			}
		})
		if target == nil {
			panic(fmt.Sprintf("no splittable leaf for pane %d", i))
		}
		p := &mux.Pane{ID: uint32(i), Meta: mux.PaneMeta{Name: fmt.Sprintf("pane-%d", i)}}
		if _, err := target.Split(dir, p); err != nil {
			panic(err)
		}
		ids = append(ids, uint32(i))
	}
	root.FixOffsets()
	return root, ids
}

// benchScreen generates a realistic 80-column screen with ANSI colors.
func benchScreen(w, h int) string {
	line := "\033[32m$ \033[0m" + strings.Repeat("x", w-2)
	lines := make([]string, h)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func BenchmarkRenderFull(b *testing.B) {
	for _, n := range []int{2, 4, 6, 8, 12} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			w, h := 200, 60
			root, _ := benchLayoutTree(n, w, h)

			// Pre-build PaneData for each pane
			paneDataMap := make(map[uint32]PaneData, n)
			root.Walk(func(c *mux.LayoutCell) {
				pid := c.CellPaneID()
				paneDataMap[pid] = &fakePaneData{
					id:     pid,
					name:   fmt.Sprintf("pane-%d", pid),
					screen: benchScreen(c.W, mux.PaneContentHeight(c.H)),
				}
			})

			lookup := func(id uint32) PaneData {
				return paneDataMap[id]
			}

			comp := NewCompositor(w, h+GlobalBarHeight, "bench")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				comp.RenderFull(root, 1, lookup)
			}
		})
	}
}

func BenchmarkBuildGridWithOverlay(b *testing.B) {
	for _, n := range []int{2, 4, 6, 8, 12} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			w, h := 200, 60
			root, paneIDs := benchLayoutTree(n, w, h)

			paneDataMap := make(map[uint32]PaneData, n)
			root.Walk(func(c *mux.LayoutCell) {
				pid := c.CellPaneID()
				paneDataMap[pid] = &fakePaneData{
					id:     pid,
					name:   fmt.Sprintf("pane-%d", pid),
					screen: benchScreen(c.W, mux.PaneContentHeight(c.H)),
				}
			})

			lookup := func(id uint32) PaneData {
				return paneDataMap[id]
			}

			comp := NewCompositor(w, h+GlobalBarHeight, "bench")
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				comp.buildGridWithOverlay(root, paneIDs[0], lookup, OverlayState{})
			}
		})
	}
}

func BenchmarkRenderDiffDirtyPanes(b *testing.B) {
	const (
		width  = 200
		height = 60
	)

	tests := []struct {
		name       string
		panes      int
		dirtyPIDs  []uint32
		fullRedraw bool
	}{
		{name: "dirty_one_of_twenty", panes: 20, dirtyPIDs: []uint32{1}, fullRedraw: false},
		{name: "full_redraw_one_of_twenty", panes: 20, dirtyPIDs: []uint32{1}, fullRedraw: true},
		{name: "dirty_four_of_twenty_five", panes: 25, dirtyPIDs: []uint32{1, 2, 3, 4}, fullRedraw: false},
		{name: "full_redraw_four_of_twenty_five", panes: 25, dirtyPIDs: []uint32{1, 2, 3, 4}, fullRedraw: true},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			root, _ := benchLayoutTree(tt.panes, width, height)
			paneDataMap := make(map[uint32]*fakePaneData, tt.panes)
			root.Walk(func(c *mux.LayoutCell) {
				pid := c.CellPaneID()
				paneDataMap[pid] = &fakePaneData{
					id:     pid,
					name:   fmt.Sprintf("pane-%d", pid),
					screen: benchScreen(c.W, mux.PaneContentHeight(c.H)),
				}
			})
			lookup := func(id uint32) PaneData { return paneDataMap[id] }

			dirtyScreensA := make(map[uint32]string, len(tt.dirtyPIDs))
			dirtyScreensB := make(map[uint32]string, len(tt.dirtyPIDs))
			dirtyPanes := make(map[uint32]struct{}, len(tt.dirtyPIDs))
			for i, pid := range tt.dirtyPIDs {
				dirtyPanes[pid] = struct{}{}
				dirtyScreensA[pid] = paneDataMap[pid].screen
				replacement := string(rune('y' + i))
				dirtyScreensB[pid] = strings.ReplaceAll(dirtyScreensA[pid], "x", replacement)
			}

			comp := NewCompositor(width, height+GlobalBarHeight, "bench")
			comp.RenderDiffWithOverlayDirty(root, 1, lookup, OverlayState{}, dirtyPanes, true)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, pid := range tt.dirtyPIDs {
					if i%2 == 0 {
						paneDataMap[pid].screen = dirtyScreensB[pid]
					} else {
						paneDataMap[pid].screen = dirtyScreensA[pid]
					}
				}
				comp.RenderDiffWithOverlayDirty(root, 1, lookup, OverlayState{}, dirtyPanes, tt.fullRedraw)
			}
		})
	}
}

func BenchmarkCompositorDirtyPaneRepresentative(b *testing.B) {
	const (
		width   = 240
		layoutH = 80
		panes   = 20
	)

	root, paneIDs := benchLayoutTree(panes, width, layoutH)
	paneDataMap := make(map[uint32]*styledPaneData, panes)
	root.Walk(func(c *mux.LayoutCell) {
		pid := c.CellPaneID()
		paneDataMap[pid] = &styledPaneData{
			fakePaneData: fakePaneData{
				id:   pid,
				name: fmt.Sprintf("pane-%d", pid),
			},
			cells: benchScreenCellGrid(c.W, mux.PaneContentHeight(c.H), "x"),
		}
	})
	lookup := func(id uint32) PaneData { return paneDataMap[id] }

	dirtyPaneID := paneIDs[0]
	dirtyCell := root.FindByPaneID(dirtyPaneID)
	dirtyPanes := map[uint32]struct{}{dirtyPaneID: {}}
	dirtyCellsA := paneDataMap[dirtyPaneID].cells
	dirtyCellsB := benchScreenCellGrid(dirtyCell.W, mux.PaneContentHeight(dirtyCell.H), "y")

	comp := NewCompositor(width, layoutH+GlobalBarHeight, "bench")
	comp.RenderDiffWithOverlayDirty(root, paneIDs[0], lookup, OverlayState{}, dirtyPanes, true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			paneDataMap[dirtyPaneID].cells = dirtyCellsB
		} else {
			paneDataMap[dirtyPaneID].cells = dirtyCellsA
		}
		comp.RenderDiffWithOverlayDirty(root, paneIDs[0], lookup, OverlayState{}, dirtyPanes, false)
	}
}

func BenchmarkBuildPaneContentCells(b *testing.B) {
	const (
		width  = 160
		height = 40
	)
	rows := benchScreenCellGrid(width, height, "x")
	cell := mux.NewLeaf(&mux.Pane{ID: 1, Meta: mux.PaneMeta{Name: "pane-1"}}, 0, 0, width, height+mux.StatusLineRows)

	tests := []struct {
		name string
		pane PaneData
	}{
		{
			name: "value_fallback",
			pane: &styledPaneData{
				fakePaneData: fakePaneData{id: 1, name: "pane-1"},
				cells:        rows,
			},
		},
		{
			name: "in_place_writer",
			pane: &inPlacePaneData{
				styledPaneData: styledPaneData{
					fakePaneData: fakePaneData{id: 1, name: "pane-1"},
					cells:        rows,
				},
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			grid := NewScreenGrid(width, height+mux.StatusLineRows)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for row := 0; row < height; row++ {
					buildPaneContentCells(grid, cell, row, true, tt.pane, nil)
				}
			}
		})
	}
}

func benchScreenCellGrid(w, h int, fill string) [][]ScreenCell {
	rows := make([][]ScreenCell, h)
	for row := range rows {
		rows[row] = make([]ScreenCell, w)
		for col := range rows[row] {
			ch := fill
			if col%17 == 0 {
				ch = "$"
			}
			if col%23 == 0 {
				ch = " "
			}
			rows[row][col] = ScreenCell{Char: ch, Width: 1}
		}
	}
	return rows
}

func BenchmarkClipLine(b *testing.B) {
	for _, width := range []int{40, 80, 200} {
		b.Run(fmt.Sprintf("width_%d", width), func(b *testing.B) {
			// Realistic ANSI line: color escapes + text
			line := "\033[38;2;166;227;161m" + strings.Repeat("A", width+50) + "\033[0m"
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				clipLine(line, width)
			}
		})
	}
}

func BenchmarkBuildBorderMap(b *testing.B) {
	for _, n := range []int{2, 4, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			w, h := 200, 60
			root, _ := benchLayoutTree(n, w, h)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				buildBorderMap(root, w, h+GlobalBarHeight)
			}
		})
	}
}
