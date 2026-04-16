package mux

import (
	"fmt"
	"testing"
)

// benchTree builds a balanced layout tree with n panes by alternating H/V splits.
func benchTree(n int) *LayoutCell {
	if n < 1 {
		n = 1
	}
	root := NewLeaf(fakePaneID(1), 0, 0, 800, 240)
	for i := 2; i <= n; i++ {
		var target *LayoutCell
		root.Walk(func(c *LayoutCell) {
			if target == nil {
				target = c
			}
		})
		dir := SplitVertical
		if i%2 == 0 {
			dir = SplitHorizontal
		}
		if _, err := target.Split(dir, fakePaneID(uint32(i))); err != nil {
			panic(err)
		}
	}
	root.FixOffsets()
	return root
}

func BenchmarkSplit(b *testing.B) {
	for _, depth := range []int{1, 4, 10} {
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				root := NewLeaf(fakePaneID(1), 0, 0, 80, 24)
				for i := 2; i <= depth; i++ {
					var target *LayoutCell
					root.Walk(func(c *LayoutCell) {
						if target == nil {
							target = c
						}
					})
					dir := SplitVertical
					if i%2 == 0 {
						dir = SplitHorizontal
					}
					if _, err := target.Split(dir, fakePaneID(uint32(i))); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkClose(b *testing.B) {
	for _, n := range []int{2, 4, 10} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				root := benchTree(n)
				// Close the last leaf
				var last *LayoutCell
				root.Walk(func(c *LayoutCell) {
					last = c
				})
				last.Close()
			}
		})
	}
}

func BenchmarkWalk(b *testing.B) {
	for _, n := range []int{1, 4, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			root := benchTree(n)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				root.Walk(func(c *LayoutCell) {})
			}
		})
	}
}

func BenchmarkFixOffsets(b *testing.B) {
	for _, n := range []int{1, 4, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			root := benchTree(n)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				root.FixOffsets()
			}
		})
	}
}

// flatHTree builds a horizontal tree with n same-direction siblings (Case A scenario).
func flatHTree(n int) *LayoutCell {
	root := NewLeaf(fakePaneID(1), 0, 0, 800, 240)
	// First split creates Case B (root has no parent), establishing the internal node
	if n >= 2 {
		if _, err := root.Split(SplitVertical, fakePaneID(2)); err != nil {
			panic(err)
		}
	}
	// Subsequent splits use Case A (same-direction sibling insertion)
	for i := 3; i <= n; i++ {
		if _, err := root.Children[0].Split(SplitVertical, fakePaneID(uint32(i))); err != nil {
			panic(err)
		}
	}
	root.FixOffsets()
	return root
}

// BenchmarkSplitIncremental measures the cost of building a flat tree by
// adding one same-direction split at a time. The per-split cost should be
// constant (O(1)) regardless of sibling count.
func BenchmarkSplitIncremental(b *testing.B) {
	for _, n := range []int{4, 10, 20, 40} {
		b.Run(fmt.Sprintf("siblings_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				flatHTree(n)
			}
		})
	}
}

func BenchmarkResolvePane(b *testing.B) {
	for _, n := range []int{1, 10, 20} {
		b.Run(fmt.Sprintf("panes_%d", n), func(b *testing.B) {
			// Build a window with named panes
			panes := make([]struct {
				id         uint32
				x, y, w, h int
			}, n)
			for i := range panes {
				panes[i] = struct {
					id         uint32
					x, y, w, h int
				}{uint32(i + 1), i * 4, 0, 4, 24}
			}
			w := buildLayout(1, panes)
			// Set Meta.Name on each pane
			w.Root.Walk(func(c *LayoutCell) {
				if c.Pane != nil {
					c.Pane.Meta.Name = fmt.Sprintf("pane-%d", c.Pane.ID)
				}
			})
			target := fmt.Sprintf("pane-%d", n)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _ = w.ResolvePane(target)
			}
		})
	}
}
