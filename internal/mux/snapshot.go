package mux

import "github.com/weill-labs/amux/internal/proto"

// SnapshotLayout creates a serializable snapshot of the current layout state.
// Used for single-window backward compatibility and by SnapshotWindow.
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
			ID:         p.ID,
			Name:       p.Meta.Name,
			Host:       p.Meta.Host,
			Task:       p.Meta.Task,
			Color:      p.Meta.Color,
			Minimized:  p.Meta.Minimized,
			ConnStatus: p.Meta.Remote,
		})
	}
	return snap
}

// SnapshotWindow creates a WindowSnapshot for the wire protocol.
func (w *Window) SnapshotWindow(index int) proto.WindowSnapshot {
	ws := proto.WindowSnapshot{
		ID:           w.ID,
		Name:         w.Name,
		Index:        index,
		ZoomedPaneID: w.ZoomedPaneID,
		Root:         snapshotCell(w.Root),
	}
	if w.ActivePane != nil {
		ws.ActivePaneID = w.ActivePane.ID
	}
	for _, p := range w.Panes() {
		ws.Panes = append(ws.Panes, proto.PaneSnapshot{
			ID:         p.ID,
			Name:       p.Meta.Name,
			Host:       p.Meta.Host,
			Task:       p.Meta.Task,
			Color:      p.Meta.Color,
			Minimized:  p.Meta.Minimized,
			ConnStatus: p.Meta.Remote,
		})
	}
	return ws
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
// attaching actual Pane pointers to leaf cells. Used for server hot-reload
// when restoring from a legacy single-window checkpoint.
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

	w := &Window{
		Root:         root,
		ActivePane:   activePane,
		Width:        snap.Width,
		Height:       snap.Height,
		ZoomedPaneID: snap.ZoomedPaneID,
	}
	w.recoverMinimizeSeq()
	return w
}

// RebuildWindowFromSnapshot creates a server-side Window from a WindowSnapshot.
func RebuildWindowFromSnapshot(ws proto.WindowSnapshot, width, height int, paneMap map[uint32]*Pane) *Window {
	root := rebuildCellWithPanes(ws.Root, paneMap)

	var activePane *Pane
	if p, ok := paneMap[ws.ActivePaneID]; ok {
		activePane = p
	} else {
		root.Walk(func(c *LayoutCell) {
			if activePane == nil && c.Pane != nil {
				activePane = c.Pane
			}
		})
	}

	w := &Window{
		ID:           ws.ID,
		Name:         ws.Name,
		Root:         root,
		ActivePane:   activePane,
		Width:        width,
		Height:       height,
		ZoomedPaneID: ws.ZoomedPaneID,
	}
	w.recoverMinimizeSeq()
	return w
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
