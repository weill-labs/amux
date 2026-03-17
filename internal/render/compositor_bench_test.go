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
		var target *mux.LayoutCell
		root.Walk(func(c *mux.LayoutCell) {
			if target == nil {
				target = c
			}
		})
		dir := mux.SplitVertical
		if i%2 == 0 {
			dir = mux.SplitHorizontal
		}
		p := &mux.Pane{ID: uint32(i), Meta: mux.PaneMeta{Name: fmt.Sprintf("pane-%d", i)}}
		target.Split(dir, p)
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
	for _, n := range []int{1, 4, 10, 20} {
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
