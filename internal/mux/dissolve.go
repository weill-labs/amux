package mux

func (w *Window) columnRoot(cell *LayoutCell) *LayoutCell {
	root := cell
	for root.Parent != nil && root.Parent.Dir == SplitHorizontal {
		root = root.Parent
	}
	return root
}

func (w *Window) columnHasVisibleLeafAfterMinimize(root *LayoutCell, paneID uint32) bool {
	visible := false
	root.Walk(func(c *LayoutCell) {
		if visible || c.Pane == nil {
			return
		}
		if c.Pane.ID == paneID {
			return
		}
		if !c.Pane.Meta.Minimized {
			visible = true
		}
	})
	return visible
}

func (w *Window) minimizeLeaf(cell *LayoutCell) {
	cell.Pane.Meta.Minimized = true
	cell.Pane.Meta.RestoreH = cell.H
	w.minimizeSeq++
	cell.Pane.Meta.MinimizedSeq = w.minimizeSeq
	cell.H = StatusLineRows
}

func (w *Window) ensureDissolveHost(slot *LayoutCell) *LayoutCell {
	if slot == nil || slot.DissolveHost {
		return slot
	}

	parent := slot.Parent
	idx := slot.IndexInParent()
	host := &LayoutCell{
		X: slot.X, Y: slot.Y, W: slot.W, H: slot.H,
		Dir:          SplitHorizontal,
		Children:     []*LayoutCell{slot},
		DissolveHost: true,
	}
	slot.Parent = host
	if parent != nil {
		host.Parent = parent
		parent.Children[idx] = host
	} else {
		w.Root = host
	}
	return host
}

func (w *Window) makeDissolveHost(base *LayoutCell, dissolved []*LayoutCell) *LayoutCell {
	if len(dissolved) == 0 {
		return base
	}
	children := make([]*LayoutCell, 0, 1+len(dissolved))
	children = append(children, base)
	children = append(children, dissolved...)
	host := &LayoutCell{
		X: base.X, Y: base.Y, W: base.W, H: base.H,
		Dir:          SplitHorizontal,
		DissolveHost: true,
		Children:     children,
	}
	base.Parent = host
	for _, child := range dissolved {
		child.Parent = host
	}
	return host
}

func (w *Window) prependDissolved(host *LayoutCell, groups []*LayoutCell) {
	if host == nil || len(groups) == 0 {
		return
	}
	children := make([]*LayoutCell, 0, len(groups)+len(host.Children))
	children = append(children, host.Children[0])
	for _, group := range groups {
		group.Parent = host
		children = append(children, group)
	}
	children = append(children, host.Children[1:]...)
	host.Children = children
}

func (w *Window) dissolveGroups(column *LayoutCell) []*LayoutCell {
	if column.DissolveHost {
		moved := append([]*LayoutCell(nil), column.Children[1:]...)
		base := column.Children[0]
		base.DissolvedColumn = true
		base.RestoreW = column.W
		moved = append(moved, base)
		return moved
	}

	column.DissolvedColumn = true
	column.RestoreW = column.W
	return []*LayoutCell{column}
}

func (w *Window) setCellSize(cell *LayoutCell, width, height int) {
	if cell == nil {
		return
	}
	if cell.IsLeaf() {
		cell.W = width
		cell.H = height
		return
	}
	cell.ResizeSubtree(width, height)
}

func (w *Window) dissolveColumn(column *LayoutCell) {
	parent := column.Parent
	idx := column.IndexInParent()
	host := w.ensureDissolveHost(parent.Children[idx+1])
	w.prependDissolved(host, w.dissolveGroups(column))

	if len(parent.Children) == 2 {
		host.X = parent.X
		host.Y = parent.Y
		host.W = parent.W
		host.H = parent.H
		if parent.Parent != nil {
			gidx := parent.IndexInParent()
			host.Parent = parent.Parent
			parent.Parent.Children[gidx] = host
		} else {
			host.Parent = nil
			w.Root = host
		}
		return
	}

	reclaimed := column.W + 1
	parent.Children = append(parent.Children[:idx], parent.Children[idx+1:]...)
	w.setCellSize(host, host.W+reclaimed, parent.H)
}

func (w *Window) dissolvedColumnRoot(cell *LayoutCell) *LayoutCell {
	for c := cell; c != nil; c = c.Parent {
		if c.DissolvedColumn {
			return c
		}
	}
	return nil
}

func splitRestoreWidths(totalW, restoreW int) (leftW, rightW int) {
	available := totalW - 1
	if available <= 1 {
		return totalW, 0
	}
	if available < 2*PaneMinSize {
		leftW = available / 2
		if leftW == 0 {
			leftW = 1
		}
		return leftW, available - leftW
	}

	leftW = restoreW
	if leftW < PaneMinSize {
		leftW = PaneMinSize
	}
	if available-leftW < PaneMinSize {
		leftW = available - PaneMinSize
	}
	return leftW, available - leftW
}

func (w *Window) reconstituteDissolvedColumn(dissolved *LayoutCell) {
	host := dissolved.Parent
	if host == nil || !host.DissolveHost {
		return
	}

	restoreW := dissolved.RestoreW
	if restoreW <= 0 {
		restoreW = dissolved.W
	}

	idx := dissolved.IndexInParent()
	leftGroups := append([]*LayoutCell(nil), host.Children[1:idx]...)
	rightGroups := append([]*LayoutCell(nil), host.Children[idx+1:]...)
	base := host.Children[0]

	dissolved.DissolvedColumn = false
	dissolved.RestoreW = 0

	visible := w.makeDissolveHost(dissolved, leftGroups)

	var hostAfter *LayoutCell
	if len(rightGroups) == 0 {
		base.Parent = host.Parent
		hostAfter = base
	} else {
		host.Children = append([]*LayoutCell{base}, rightGroups...)
		for _, child := range host.Children {
			child.Parent = host
		}
		hostAfter = host
	}

	parent := host.Parent
	leftW, rightW := splitRestoreWidths(host.W, restoreW)
	if parent == nil {
		w.setCellSize(visible, leftW, host.H)
		w.setCellSize(hostAfter, rightW, host.H)
		newRoot := &LayoutCell{
			X: 0, Y: 0, W: w.Width, H: w.Height,
			Dir:      SplitVertical,
			Children: []*LayoutCell{visible, hostAfter},
		}
		visible.Parent = newRoot
		hostAfter.Parent = newRoot
		w.Root = newRoot
		return
	}

	hostIdx := host.IndexInParent()
	w.setCellSize(visible, leftW, parent.H)
	w.setCellSize(hostAfter, rightW, parent.H)
	visible.Parent = parent
	hostAfter.Parent = parent

	children := append([]*LayoutCell{}, parent.Children[:hostIdx]...)
	children = append(children, visible, hostAfter)
	children = append(children, parent.Children[hostIdx+1:]...)
	parent.Children = children
}
