package mux

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

const (
	invariantSequenceSteps = 80
	invariantMaxPanes      = 12
)

var invariantSeeds = []int64{1, 2, 3, 5, 8, 13, 21, 34}

func TestSplitRootRejectsLayoutsBelowMinimumSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		window     func(t *testing.T) *Window
		dir        SplitDir
		wantPanes  int
		wantErr    string
		wantTrace  string
		nextPaneID uint32
	}{
		{
			name: "leaf root too narrow for vertical split",
			window: func(t *testing.T) *Window {
				t.Helper()
				return NewWindow(invariantPane(1), 4, 10)
			},
			dir:        SplitVertical,
			wantPanes:  1,
			wantErr:    "not enough space to split",
			wantTrace:  "4 < 5",
			nextPaneID: 2,
		},
		{
			name: "same direction root append would overflow columns",
			window: func(t *testing.T) *Window {
				t.Helper()
				w := NewWindow(invariantPane(1), 8, 10)
				if _, err := w.SplitRoot(SplitVertical, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitRoot(SplitVertical, invariantPane(3)); err != nil {
					t.Fatalf("SplitRoot pane-3: %v", err)
				}
				return w
			},
			dir:        SplitVertical,
			wantPanes:  3,
			wantErr:    "not enough space to split",
			wantTrace:  "8 < 11",
			nextPaneID: 4,
		},
		{
			name: "wrapped root split would overflow existing subtree",
			window: func(t *testing.T) *Window {
				t.Helper()
				w := NewWindow(invariantPane(1), 10, 12)
				if _, err := w.SplitRoot(SplitVertical, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitRoot(SplitVertical, invariantPane(3)); err != nil {
					t.Fatalf("SplitRoot pane-3: %v", err)
				}
				if _, err := w.SplitRoot(SplitHorizontal, invariantPane(4)); err != nil {
					t.Fatalf("SplitRoot pane-4: %v", err)
				}
				return w
			},
			dir:        SplitVertical,
			wantPanes:  4,
			wantErr:    "not enough space to split",
			wantTrace:  "10 < 11",
			nextPaneID: 5,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := tt.window(t)
			before := snapshotLeafGeometry(w.Root)
			_, err := w.SplitRoot(tt.dir, invariantPane(tt.nextPaneID))
			if err == nil {
				t.Fatal("SplitRoot error = nil, want not enough space")
			}
			if !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), tt.wantTrace) {
				t.Fatalf("SplitRoot error = %q, want %q with %q", err.Error(), tt.wantErr, tt.wantTrace)
			}
			if got := w.PaneCount(); got != tt.wantPanes {
				t.Fatalf("PaneCount after failed SplitRoot = %d, want %d", got, tt.wantPanes)
			}
			if diff := diffLeafGeometry(before, snapshotLeafGeometry(w.Root)); diff != "" {
				t.Fatalf("failed SplitRoot mutated layout:\n%s", diff)
			}
			assertGeometryInvariant(t, w, []string{tt.name})
		})
	}
}

func TestResizeAllPreservesNestedSubtreeMinimums(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		build      func(t *testing.T) *Window
		width      int
		height     int
		violatedID uint32
		axis       SplitDir
	}{
		{
			name: "vertical shrink keeps nested column wide enough",
			build: func(t *testing.T) *Window {
				t.Helper()

				w := NewWindow(invariantPane(1), 20, 12)
				if _, err := w.SplitRoot(SplitVertical, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitHorizontal, invariantPane(3), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-3: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitVertical, invariantPane(4), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-4: %v", err)
				}
				return w
			},
			width:      8,
			height:     12,
			violatedID: 2,
			axis:       SplitVertical,
		},
		{
			name: "horizontal shrink keeps nested row tall enough",
			build: func(t *testing.T) *Window {
				t.Helper()

				w := NewWindow(invariantPane(1), 12, 20)
				if _, err := w.SplitRoot(SplitHorizontal, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitVertical, invariantPane(3), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-3: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitHorizontal, invariantPane(4), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-4: %v", err)
				}
				return w
			},
			width:      12,
			height:     8,
			violatedID: 2,
			axis:       SplitHorizontal,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := tt.build(t)
			w.Resize(tt.width, tt.height)
			cell := w.Root.FindPane(tt.violatedID)
			if cell == nil || cell.Parent == nil {
				t.Fatalf("nested pane-%d cell = %v, want nested cell", tt.violatedID, cell)
			}
			parent := cell.Parent
			if got, want := parent.axisSize(tt.axis), parent.minSubtreeSize(tt.axis); got < want {
				t.Fatalf("nested subtree %s size = %d, want at least %d", splitDirName(tt.axis), got, want)
			}
			assertGeometryInvariant(t, w, []string{tt.name})
		})
	}
}

func TestSplitSiblingInsertionPreservesNestedSubtreeMinimums(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		build func(t *testing.T) (*Window, *Pane, SplitDir)
	}{
		{
			name: "vertical sibling insertion keeps nested column wide enough",
			build: func(t *testing.T) (*Window, *Pane, SplitDir) {
				t.Helper()

				p1 := invariantPane(1)
				w := NewWindow(p1, 14, 12)
				if _, err := w.SplitRoot(SplitVertical, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitHorizontal, invariantPane(3), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-3: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitVertical, invariantPane(4), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-4: %v", err)
				}
				w.FocusPane(p1)
				return w, invariantPane(5), SplitVertical
			},
		},
		{
			name: "horizontal sibling insertion keeps nested row tall enough",
			build: func(t *testing.T) (*Window, *Pane, SplitDir) {
				t.Helper()

				p1 := invariantPane(1)
				w := NewWindow(p1, 12, 14)
				if _, err := w.SplitRoot(SplitHorizontal, invariantPane(2)); err != nil {
					t.Fatalf("SplitRoot pane-2: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitVertical, invariantPane(3), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-3: %v", err)
				}
				if _, err := w.SplitPaneWithOptions(2, SplitHorizontal, invariantPane(4), SplitOptions{}); err != nil {
					t.Fatalf("SplitPaneWithOptions pane-4: %v", err)
				}
				w.FocusPane(p1)
				return w, invariantPane(5), SplitHorizontal
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w, pane, dir := tt.build(t)
			if _, err := w.Split(dir, pane); err != nil {
				t.Fatalf("Split(%s): %v", splitDirName(dir), err)
			}
			assertGeometryInvariant(t, w, []string{tt.name})
		})
	}
}

func TestWindowLayoutInvariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, w *Window, trace []string)
	}{
		{
			name:  "geometry_covers_parent_rectangles",
			check: assertGeometryInvariant,
		},
		{
			name:  "walk_visits_each_leaf_once",
			check: assertWalkInvariant,
		},
		{
			name:  "pane_collections_match_tree",
			check: assertPaneCollectionInvariant,
		},
		{
			name:  "resolve_name_and_id_match",
			check: assertResolvePaneInvariant,
		},
		{
			name:  "active_pane_survives",
			check: assertActivePaneInvariant,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, seed := range invariantSeeds {
				runInvariantSequence(t, seed, tt.check)
			}
		})
	}
}

func runInvariantSequence(t *testing.T, seed int64, check func(t *testing.T, w *Window, trace []string)) {
	t.Helper()

	rng := rand.New(rand.NewSource(seed))
	w := NewWindow(invariantPane(1), 80, 24)
	nextPaneID := uint32(2)
	trace := []string{fmt.Sprintf("seed=%d new-window 80x24 pane-1", seed)}

	check(t, w, trace)
	for step := 0; step < invariantSequenceSteps; step++ {
		op := applyInvariantOperation(t, rng, w, &nextPaneID)
		trace = append(trace, fmt.Sprintf("%02d %s", step, op))
		check(t, w, trace)
	}
}

func applyInvariantOperation(t *testing.T, rng *rand.Rand, w *Window, nextPaneID *uint32) string {
	t.Helper()

	switch rng.Intn(6) {
	case 0:
		dir := randomSplitDir(rng)
		if w.PaneCount() >= invariantMaxPanes {
			return fmt.Sprintf("split-active %s skipped at pane limit", splitDirName(dir))
		}
		pane := invariantPane(*nextPaneID)
		*nextPaneID++
		_, err := w.Split(dir, pane)
		if err != nil {
			return fmt.Sprintf("split-active %s pane-%d error=%q", splitDirName(dir), pane.ID, err.Error())
		}
		return fmt.Sprintf("split-active %s pane-%d", splitDirName(dir), pane.ID)
	case 1:
		dir := randomSplitDir(rng)
		if w.PaneCount() >= invariantMaxPanes {
			return fmt.Sprintf("split-root %s skipped at pane limit", splitDirName(dir))
		}
		pane := invariantPane(*nextPaneID)
		*nextPaneID++
		_, err := w.SplitRoot(dir, pane)
		if err != nil {
			return fmt.Sprintf("split-root %s pane-%d error=%q", splitDirName(dir), pane.ID, err.Error())
		}
		return fmt.Sprintf("split-root %s pane-%d", splitDirName(dir), pane.ID)
	case 2:
		panes := w.Panes()
		if len(panes) <= 1 {
			return "close skipped at one pane"
		}
		pane := panes[rng.Intn(len(panes))]
		if err := w.ClosePane(pane.ID); err != nil {
			t.Fatalf("ClosePane(%d): %v\ntrace:\n%s", pane.ID, err, strings.Join([]string{fmt.Sprintf("close pane-%d", pane.ID)}, "\n"))
		}
		return fmt.Sprintf("close pane-%d", pane.ID)
	case 3:
		directions := []string{"next", "left", "right", "up", "down"}
		dir := directions[rng.Intn(len(directions))]
		before := uint32(0)
		if w.ActivePane != nil {
			before = w.ActivePane.ID
		}
		w.Focus(dir)
		after := uint32(0)
		if w.ActivePane != nil {
			after = w.ActivePane.ID
		}
		return fmt.Sprintf("focus %s pane-%d->pane-%d", dir, before, after)
	case 4:
		minW := w.Root.minSubtreeSize(SplitVertical)
		minH := w.Root.minSubtreeSize(SplitHorizontal)
		width := minW + rng.Intn(90)
		height := minH + rng.Intn(36)
		w.Resize(width, height)
		return fmt.Sprintf("resize-window %dx%d", width, height)
	default:
		panes := w.Panes()
		pane := panes[rng.Intn(len(panes))]
		directions := []string{"left", "right", "up", "down"}
		dir := directions[rng.Intn(len(directions))]
		delta := 1 + rng.Intn(8)
		ok := w.ResizePane(pane.ID, dir, delta)
		return fmt.Sprintf("resize-pane pane-%d %s %d ok=%t", pane.ID, dir, delta, ok)
	}
}

func invariantPane(id uint32) *Pane {
	return &Pane{
		ID: id,
		Meta: PaneMeta{
			Name: fmt.Sprintf("pane-%d", id),
			Host: DefaultHost,
		},
	}
}

func randomSplitDir(rng *rand.Rand) SplitDir {
	if rng.Intn(2) == 0 {
		return SplitVertical
	}
	return SplitHorizontal
}

func splitDirName(dir SplitDir) string {
	if dir == SplitVertical {
		return "vertical"
	}
	return "horizontal"
}

func assertGeometryInvariant(t *testing.T, w *Window, trace []string) {
	t.Helper()

	if w.Root == nil {
		invariantFatalf(t, trace, "window root is nil")
	}
	if w.Root.Parent != nil {
		invariantFatalf(t, trace, "root parent = %p, want nil", w.Root.Parent)
	}
	if w.Root.X != 0 || w.Root.Y != 0 {
		invariantFatalf(t, trace, "root offset = (%d,%d), want (0,0)", w.Root.X, w.Root.Y)
	}
	if w.Root.W != w.Width || w.Root.H != w.Height {
		invariantFatalf(t, trace, "root size = %dx%d, want window size %dx%d", w.Root.W, w.Root.H, w.Width, w.Height)
	}

	assertCellGeometryInvariant(t, w.Root, trace)

	leaves := collectLeavesByWalk(w.Root)
	for i := 0; i < len(leaves); i++ {
		for j := i + 1; j < len(leaves); j++ {
			if cellsOverlap(leaves[i], leaves[j]) {
				invariantFatalf(t, trace, "pane-%d overlaps pane-%d: %s vs %s",
					leaves[i].CellPaneID(), leaves[j].CellPaneID(),
					cellRect(leaves[i]), cellRect(leaves[j]))
			}
		}
	}
}

func assertCellGeometryInvariant(t *testing.T, cell *LayoutCell, trace []string) {
	t.Helper()

	if cell.W <= 0 || cell.H <= 0 {
		invariantFatalf(t, trace, "cell %s has non-positive size", cellRect(cell))
	}
	if cell.IsLeaf() {
		if len(cell.Children) != 0 {
			invariantFatalf(t, trace, "leaf pane-%d has %d children", cell.CellPaneID(), len(cell.Children))
		}
		if cell.CellPaneID() == 0 {
			invariantFatalf(t, trace, "leaf %s has no pane identity", cellRect(cell))
		}
		if cell.W < PaneMinSize || cell.H < PaneMinSize {
			invariantFatalf(t, trace, "leaf pane-%d size = %dx%d, want each dimension >= %d",
				cell.CellPaneID(), cell.W, cell.H, PaneMinSize)
		}
		return
	}

	if cell.Pane != nil || cell.PaneID != 0 {
		invariantFatalf(t, trace, "internal cell %s has pane identity pane=%v paneID=%d", cellRect(cell), cell.Pane, cell.PaneID)
	}
	if len(cell.Children) < 2 {
		invariantFatalf(t, trace, "internal cell %s has %d children, want at least 2", cellRect(cell), len(cell.Children))
	}
	if cell.Dir != SplitVertical && cell.Dir != SplitHorizontal {
		invariantFatalf(t, trace, "internal cell %s dir = %d, want split direction", cellRect(cell), cell.Dir)
	}
	if cell.W < cell.minSubtreeSize(SplitVertical) {
		invariantFatalf(t, trace, "cell %s width below subtree minimum %d", cellRect(cell), cell.minSubtreeSize(SplitVertical))
	}
	if cell.H < cell.minSubtreeSize(SplitHorizontal) {
		invariantFatalf(t, trace, "cell %s height below subtree minimum %d", cellRect(cell), cell.minSubtreeSize(SplitHorizontal))
	}

	total := len(cell.Children) - 1
	if cell.Dir == SplitVertical {
		x := cell.X
		for i, child := range cell.Children {
			if child.Parent != cell {
				invariantFatalf(t, trace, "child[%d] parent = %p, want %p", i, child.Parent, cell)
			}
			if child.X != x || child.Y != cell.Y {
				invariantFatalf(t, trace, "child[%d] offset = (%d,%d), want (%d,%d)", i, child.X, child.Y, x, cell.Y)
			}
			if child.H != cell.H {
				invariantFatalf(t, trace, "child[%d] height = %d, want parent height %d", i, child.H, cell.H)
			}
			total += child.W
			x += child.W + 1
			assertCellGeometryInvariant(t, child, trace)
		}
		if total != cell.W {
			invariantFatalf(t, trace, "vertical children total = %d, want parent width %d for %s", total, cell.W, cellRect(cell))
		}
		return
	}

	y := cell.Y
	for i, child := range cell.Children {
		if child.Parent != cell {
			invariantFatalf(t, trace, "child[%d] parent = %p, want %p", i, child.Parent, cell)
		}
		if child.X != cell.X || child.Y != y {
			invariantFatalf(t, trace, "child[%d] offset = (%d,%d), want (%d,%d)", i, child.X, child.Y, cell.X, y)
		}
		if child.W != cell.W {
			invariantFatalf(t, trace, "child[%d] width = %d, want parent width %d", i, child.W, cell.W)
		}
		total += child.H
		y += child.H + 1
		assertCellGeometryInvariant(t, child, trace)
	}
	if total != cell.H {
		invariantFatalf(t, trace, "horizontal children total = %d, want parent height %d for %s", total, cell.H, cellRect(cell))
	}
}

func assertWalkInvariant(t *testing.T, w *Window, trace []string) {
	t.Helper()

	recursive := collectLeavesRecursive(w.Root)
	walked := collectLeavesByWalk(w.Root)
	if len(walked) != len(recursive) {
		invariantFatalf(t, trace, "Walk visited %d leaves, recursive traversal found %d", len(walked), len(recursive))
	}

	seen := make(map[*LayoutCell]bool, len(walked))
	for _, leaf := range walked {
		if seen[leaf] {
			invariantFatalf(t, trace, "Walk visited pane-%d leaf more than once", leaf.CellPaneID())
		}
		seen[leaf] = true
	}
	for _, leaf := range recursive {
		if !seen[leaf] {
			invariantFatalf(t, trace, "Walk skipped pane-%d leaf", leaf.CellPaneID())
		}
	}
	for _, leaf := range walked {
		if got := w.Root.FindByPaneID(leaf.CellPaneID()); got != leaf {
			invariantFatalf(t, trace, "FindByPaneID(%d) = %p, want %p", leaf.CellPaneID(), got, leaf)
		}
		if leaf.Pane != nil {
			if got := w.Root.FindPane(leaf.Pane.ID); got != leaf {
				invariantFatalf(t, trace, "FindPane(%d) = %p, want %p", leaf.Pane.ID, got, leaf)
			}
		}
	}
}

func assertPaneCollectionInvariant(t *testing.T, w *Window, trace []string) {
	t.Helper()

	panes := w.Panes()
	if got := w.PaneCount(); got != len(panes) {
		invariantFatalf(t, trace, "PaneCount() = %d, len(Panes()) = %d", got, len(panes))
	}

	leaves := collectLeavesByWalk(w.Root)
	if len(leaves) != len(panes) {
		invariantFatalf(t, trace, "layout leaves = %d, Panes() = %d", len(leaves), len(panes))
	}

	paneByID := make(map[uint32]*Pane, len(panes))
	for _, pane := range panes {
		if pane == nil {
			invariantFatalf(t, trace, "Panes() returned nil pane")
		}
		if existing := paneByID[pane.ID]; existing != nil {
			invariantFatalf(t, trace, "duplicate pane ID %d in Panes(): %p and %p", pane.ID, existing, pane)
		}
		paneByID[pane.ID] = pane
		cell := w.Root.FindPane(pane.ID)
		if cell == nil {
			invariantFatalf(t, trace, "Panes() included pane-%d but FindPane returned nil", pane.ID)
		}
		if cell.Pane != pane {
			invariantFatalf(t, trace, "FindPane(%d).Pane = %p, want %p", pane.ID, cell.Pane, pane)
		}
	}

	for _, leaf := range leaves {
		if leaf.Pane == nil {
			invariantFatalf(t, trace, "server-side layout leaf has no Pane pointer: %s", cellRect(leaf))
		}
		if paneByID[leaf.Pane.ID] != leaf.Pane {
			invariantFatalf(t, trace, "leaf pane-%d missing from Panes()", leaf.Pane.ID)
		}
	}
}

func assertResolvePaneInvariant(t *testing.T, w *Window, trace []string) {
	t.Helper()

	for _, pane := range w.Panes() {
		byID, err := w.ResolvePane(strconv.FormatUint(uint64(pane.ID), 10))
		if err != nil {
			invariantFatalf(t, trace, "ResolvePane(%d): %v", pane.ID, err)
		}
		if byID != pane {
			invariantFatalf(t, trace, "ResolvePane(%d) = %p, want %p", pane.ID, byID, pane)
		}
		if pane.Meta.Name == "" {
			continue
		}
		byName, err := w.ResolvePane(pane.Meta.Name)
		if err != nil {
			invariantFatalf(t, trace, "ResolvePane(%q): %v", pane.Meta.Name, err)
		}
		if byName != pane {
			invariantFatalf(t, trace, "ResolvePane(%q) = %p, want %p", pane.Meta.Name, byName, pane)
		}
	}
}

func assertActivePaneInvariant(t *testing.T, w *Window, trace []string) {
	t.Helper()

	if w.ActivePane == nil {
		invariantFatalf(t, trace, "ActivePane is nil")
	}
	if got := w.Root.FindPane(w.ActivePane.ID); got == nil {
		invariantFatalf(t, trace, "ActivePane pane-%d is not in the layout", w.ActivePane.ID)
	}
	if w.ZoomedPaneID != 0 && w.Root.FindPane(w.ZoomedPaneID) == nil {
		invariantFatalf(t, trace, "ZoomedPaneID pane-%d is not in the layout", w.ZoomedPaneID)
	}
	if w.LeadPaneID != 0 && w.Root.FindPane(w.LeadPaneID) == nil {
		invariantFatalf(t, trace, "LeadPaneID pane-%d is not in the layout", w.LeadPaneID)
	}
}

func collectLeavesByWalk(root *LayoutCell) []*LayoutCell {
	var leaves []*LayoutCell
	root.Walk(func(cell *LayoutCell) {
		leaves = append(leaves, cell)
	})
	return leaves
}

func collectLeavesRecursive(root *LayoutCell) []*LayoutCell {
	if root == nil {
		return nil
	}
	if root.IsLeaf() {
		return []*LayoutCell{root}
	}
	var leaves []*LayoutCell
	for _, child := range root.Children {
		leaves = append(leaves, collectLeavesRecursive(child)...)
	}
	return leaves
}

func cellsOverlap(a, b *LayoutCell) bool {
	return a.X < b.X+b.W &&
		b.X < a.X+a.W &&
		a.Y < b.Y+b.H &&
		b.Y < a.Y+a.H
}

func cellRect(cell *LayoutCell) string {
	return fmt.Sprintf("(x=%d y=%d w=%d h=%d)", cell.X, cell.Y, cell.W, cell.H)
}

func invariantFatalf(t *testing.T, trace []string, format string, args ...any) {
	t.Helper()
	t.Fatalf(format+"\ntrace:\n%s", append(args, strings.Join(trace, "\n"))...)
}
