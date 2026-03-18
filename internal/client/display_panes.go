package client

import (
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

const displayPaneLabelAlphabet = "123456789abcdefghijklmnopqrstuvwxyz"

type displayPanesState struct {
	labels  []render.PaneOverlayLabel
	targets map[byte]uint32
}

// DisplayPanesActive reports whether the pane overlay is active.
func (cr *ClientRenderer) DisplayPanesActive() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.displayPanes != nil
}

// ShowDisplayPanes activates the pane overlay for the active layout.
// Returns false when there is no layout or too many panes to label.
func (cr *ClientRenderer) ShowDisplayPanes() bool {
	layout := cr.renderer.VisibleLayout()
	if layout == nil {
		return false
	}

	var paneIDs []uint32
	layout.Walk(func(cell *mux.LayoutCell) {
		paneIDs = append(paneIDs, cell.CellPaneID())
	})
	if len(paneIDs) == 0 || len(paneIDs) > len(displayPaneLabelAlphabet) {
		return false
	}

	labels := make([]render.PaneOverlayLabel, 0, len(paneIDs))
	targets := make(map[byte]uint32, len(paneIDs))
	for i, paneID := range paneIDs {
		key := displayPaneLabelAlphabet[i]
		labels = append(labels, render.PaneOverlayLabel{
			PaneID: paneID,
			Label:  string(key),
		})
		targets[key] = paneID
	}

	cr.mu.Lock()
	defer cr.mu.Unlock()
	cr.displayPanes = &displayPanesState{labels: labels, targets: targets}
	cr.dirty = true
	return true
}

// HideDisplayPanes clears the pane overlay.
func (cr *ClientRenderer) HideDisplayPanes() {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.displayPanes == nil {
		return
	}
	cr.displayPanes = nil
	cr.dirty = true
}

// ResolveDisplayPaneKey resolves a single key byte against the active pane
// overlay label set.
func (cr *ClientRenderer) ResolveDisplayPaneKey(b byte) (uint32, bool) {
	if b >= 'A' && b <= 'Z' {
		b = b - 'A' + 'a'
	}
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.displayPanes == nil {
		return 0, false
	}
	paneID, ok := cr.displayPanes.targets[b]
	return paneID, ok
}

func (cr *ClientRenderer) overlayLabels() []render.PaneOverlayLabel {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.displayPanes == nil {
		return nil
	}
	labels := make([]render.PaneOverlayLabel, len(cr.displayPanes.labels))
	copy(labels, cr.displayPanes.labels)
	return labels
}
