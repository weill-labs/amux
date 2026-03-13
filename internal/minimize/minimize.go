package minimize

import (
	"fmt"
	"strconv"

	"github.com/weill-labs/amux/internal/tmux"
)

// Minimize shrinks a pane to 1 row, saving its current height.
func Minimize(t tmux.Tmux, paneID string) error {
	// Check if already minimized
	val, _ := t.GetOption(paneID, "@amux_minimized")
	if val == "1" {
		return fmt.Errorf("pane %s is already minimized", paneID)
	}

	// Guard: ensure at least one other pane in the window stays non-minimized
	windowPanes, err := t.WindowPanes(paneID)
	if err != nil {
		return fmt.Errorf("listing window panes: %w", err)
	}
	nonMinimized := 0
	for _, wp := range windowPanes {
		if wp == paneID {
			continue
		}
		v, _ := t.GetOption(wp, "@amux_minimized")
		if v != "1" {
			nonMinimized++
		}
	}
	if nonMinimized == 0 {
		return fmt.Errorf("cannot minimize: %s is the last non-minimized pane in its window", paneID)
	}

	// Save current height
	height, err := t.PaneHeight(paneID)
	if err != nil {
		return fmt.Errorf("getting pane height: %w", err)
	}
	if err := t.SetOption(paneID, "@amux_restore_h", strconv.Itoa(height)); err != nil {
		return fmt.Errorf("saving restore height: %w", err)
	}

	// Resize to 1 row
	if err := t.ResizePane(paneID, 1); err != nil {
		return fmt.Errorf("resizing pane: %w", err)
	}

	// Mark minimized
	if err := t.SetOption(paneID, "@amux_minimized", "1"); err != nil {
		return fmt.Errorf("setting minimized flag: %w", err)
	}

	return nil
}

// Restore expands a minimized pane back to its saved height.
func Restore(t tmux.Tmux, paneID string) error {
	val, _ := t.GetOption(paneID, "@amux_minimized")
	if val != "1" {
		return fmt.Errorf("pane %s is not minimized", paneID)
	}

	// Read saved height
	heightStr, err := t.GetOption(paneID, "@amux_restore_h")
	if err != nil || heightStr == "" {
		heightStr = "20" // reasonable default
	}
	height, err := strconv.Atoi(heightStr)
	if err != nil {
		height = 20
	}

	// Restore size
	if err := t.ResizePane(paneID, height); err != nil {
		return fmt.Errorf("resizing pane: %w", err)
	}

	// Clear minimized state
	if err := t.SetOption(paneID, "@amux_minimized", ""); err != nil {
		return fmt.Errorf("clearing minimized flag: %w", err)
	}
	if err := t.SetOption(paneID, "@amux_restore_h", ""); err != nil {
		return fmt.Errorf("clearing restore height: %w", err)
	}

	return nil
}
