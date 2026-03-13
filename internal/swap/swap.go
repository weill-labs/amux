package swap

import (
	"fmt"

	"github.com/weill-labs/amux/internal/tmux"
)

// SwapWithMeta swaps two panes' content and copies all @amux_* metadata between them.
// tmux swap-pane only swaps content, not user options — so we manually copy metadata.
func SwapWithMeta(t tmux.Tmux, paneA, paneB string) error {
	// Read metadata from both panes
	metaA := make(map[string]string)
	metaB := make(map[string]string)
	for _, key := range tmux.AmuxOptions {
		valA, _ := t.GetOption(paneA, key)
		metaA[key] = valA
		valB, _ := t.GetOption(paneB, key)
		metaB[key] = valB
	}

	// Swap the pane content
	if err := t.SwapPane(paneA, paneB); err != nil {
		return fmt.Errorf("swapping panes: %w", err)
	}

	// Copy A's metadata to B (since A's content is now in B)
	for key, val := range metaA {
		if err := t.SetOption(paneB, key, val); err != nil {
			return fmt.Errorf("setting %s on %s: %w", key, paneB, err)
		}
	}

	// Copy B's metadata to A
	for key, val := range metaB {
		if err := t.SetOption(paneA, key, val); err != nil {
			return fmt.Errorf("setting %s on %s: %w", key, paneA, err)
		}
	}

	return nil
}
