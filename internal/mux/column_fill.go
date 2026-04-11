package mux

import (
	"fmt"
	"sort"
)

// ColumnFillSpawnPlan describes the next column-fill spawn mutation.
type ColumnFillSpawnPlan struct {
	InheritPaneID     uint32
	SplitTargetPaneID uint32
	RootSplit         bool
}

type columnFillColumn struct {
	x, w   int
	leaves []*LayoutCell
}

// PlanColumnFillSpawn picks the next column-fill spawn target. Anchored lead
// layouts operate on the logical root, leaving the lead column untouched.
func (w *Window) PlanColumnFillSpawn() (ColumnFillSpawnPlan, error) {
	w.assertOwner("PlanColumnFillSpawn")

	root := w.logicalRoot()
	if root == nil {
		return ColumnFillSpawnPlan{}, fmt.Errorf("no layout")
	}

	columns := collectColumnFillColumns(root)
	if len(columns) == 0 {
		return ColumnFillSpawnPlan{}, fmt.Errorf("no panes in layout")
	}

	maxPanesPerColumn := len(columns) + 1
	var target *columnFillColumn
	for i := range columns {
		column := &columns[i]
		if len(column.leaves) >= maxPanesPerColumn {
			continue
		}
		if target == nil || len(column.leaves) < len(target.leaves) {
			target = column
		}
	}
	if target != nil {
		bottom := target.leaves[len(target.leaves)-1]
		return ColumnFillSpawnPlan{
			InheritPaneID:     bottom.Pane.ID,
			SplitTargetPaneID: bottom.Pane.ID,
		}, nil
	}

	lastColumn := columns[len(columns)-1]
	bottom := lastColumn.leaves[len(lastColumn.leaves)-1]
	return ColumnFillSpawnPlan{
		InheritPaneID: bottom.Pane.ID,
		RootSplit:     true,
	}, nil
}

func collectColumnFillColumns(root *LayoutCell) []columnFillColumn {
	if root == nil {
		return nil
	}

	leaves := make([]*LayoutCell, 0)
	root.Walk(func(cell *LayoutCell) {
		if cell == nil || cell.Pane == nil {
			return
		}
		leaves = append(leaves, cell)
	})
	if len(leaves) == 0 {
		return nil
	}

	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].X != leaves[j].X {
			return leaves[i].X < leaves[j].X
		}
		if leaves[i].W != leaves[j].W {
			return leaves[i].W < leaves[j].W
		}
		if leaves[i].Y != leaves[j].Y {
			return leaves[i].Y < leaves[j].Y
		}
		return leaves[i].Pane.ID < leaves[j].Pane.ID
	})

	columns := make([]columnFillColumn, 0, len(leaves))
	for _, leaf := range leaves {
		last := len(columns) - 1
		if last >= 0 && columns[last].x == leaf.X && columns[last].w == leaf.W {
			columns[last].leaves = append(columns[last].leaves, leaf)
			continue
		}
		columns = append(columns, columnFillColumn{
			x:      leaf.X,
			w:      leaf.W,
			leaves: []*LayoutCell{leaf},
		})
	}
	return columns
}
