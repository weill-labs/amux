package mux

import "github.com/weill-labs/amux/internal/proto"

// SnapshotLayout creates a serializable snapshot of the current layout state.
func (w *Window) SnapshotLayout(sessionName string) *proto.LayoutSnapshot {
	snap := &proto.LayoutSnapshot{
		SessionName:  sessionName,
		Width:        w.Width,
		Height:       w.Height,
		ZoomedPaneID: w.ZoomedPaneID,
	}
	if w.ActivePane != nil {
		snap.ActivePaneID = w.ActivePane.ID
	}
	snap.Root = snapshotCell(w.Root)
	for _, p := range w.Panes() {
		snap.Panes = append(snap.Panes, proto.PaneSnapshot{
			ID:        p.ID,
			Name:      p.Meta.Name,
			Host:      p.Meta.Host,
			Task:      p.Meta.Task,
			Color:     p.Meta.Color,
			Minimized: p.Meta.Minimized,
		})
	}
	return snap
}

func snapshotCell(c *LayoutCell) proto.CellSnapshot {
	cs := proto.CellSnapshot{
		X: c.X, Y: c.Y, W: c.W, H: c.H,
		IsLeaf: c.IsLeaf(),
		Dir:    -1,
	}
	if !c.IsLeaf() {
		cs.Dir = int(c.Dir)
	}
	if c.IsLeaf() && c.Pane != nil {
		cs.PaneID = c.Pane.ID
	}
	for _, child := range c.Children {
		cs.Children = append(cs.Children, snapshotCell(child))
	}
	return cs
}

// RebuildLayout creates a LayoutCell tree from a CellSnapshot.
// Leaves store PaneID but have no Pane pointer — the client uses PaneID
// to look up its local emulator.
func RebuildLayout(cs proto.CellSnapshot) *LayoutCell {
	cell := &LayoutCell{
		X: cs.X, Y: cs.Y, W: cs.W, H: cs.H,
		isLeaf: cs.IsLeaf,
		Dir:    SplitDir(cs.Dir),
		PaneID: cs.PaneID,
	}
	for _, childSnap := range cs.Children {
		child := RebuildLayout(childSnap)
		child.Parent = cell
		cell.Children = append(cell.Children, child)
	}
	return cell
}

// RebuildFromSnapshot creates a server-side Window from a LayoutSnapshot,
// attaching actual Pane pointers to leaf cells. Used for server hot-reload.
func RebuildFromSnapshot(snap proto.LayoutSnapshot, paneMap map[uint32]*Pane) *Window {
	root := rebuildCellWithPanes(snap.Root, paneMap)

	var activePane *Pane
	if p, ok := paneMap[snap.ActivePaneID]; ok {
		activePane = p
	} else {
		// Fallback: pick any pane
		for _, p := range paneMap {
			activePane = p
			break
		}
	}

	return &Window{
		Root:         root,
		ActivePane:   activePane,
		Width:        snap.Width,
		Height:       snap.Height,
		ZoomedPaneID: snap.ZoomedPaneID,
	}
}

func rebuildCellWithPanes(cs proto.CellSnapshot, paneMap map[uint32]*Pane) *LayoutCell {
	cell := &LayoutCell{
		X: cs.X, Y: cs.Y, W: cs.W, H: cs.H,
		isLeaf: cs.IsLeaf,
		Dir:    SplitDir(cs.Dir),
		PaneID: cs.PaneID,
	}
	if cs.IsLeaf {
		if p, ok := paneMap[cs.PaneID]; ok {
			cell.Pane = p
		}
	}
	for _, childSnap := range cs.Children {
		child := rebuildCellWithPanes(childSnap, paneMap)
		child.Parent = cell
		cell.Children = append(cell.Children, child)
	}
	return cell
}
