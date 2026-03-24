package mux

import (
	"fmt"
	"strconv"
	"strings"
)

// PaneRefCandidate is the minimal identity needed to resolve a pane reference.
type PaneRefCandidate struct {
	ID   uint32
	Name string
}

// ResolvePaneRef resolves a pane reference against the provided candidates.
// Numeric refs resolve by pane ID. All other refs must match exactly one pane
// name; duplicate exact-name matches are rejected as ambiguous.
func ResolvePaneRef(ref string, candidates []PaneRefCandidate) (uint32, error) {
	if id, err := strconv.ParseUint(ref, 10, 32); err == nil {
		paneID := uint32(id)
		for _, candidate := range candidates {
			if candidate.ID == paneID {
				return paneID, nil
			}
		}
		return 0, fmt.Errorf("pane %q not found", ref)
	}

	matches := make([]PaneRefCandidate, 0, 2)
	for _, candidate := range candidates {
		if candidate.Name == ref {
			matches = append(matches, candidate)
		}
	}
	switch len(matches) {
	case 0:
		return 0, fmt.Errorf("pane %q not found", ref)
	case 1:
		return matches[0].ID, nil
	default:
		parts := make([]string, 0, len(matches))
		for _, match := range matches {
			parts = append(parts, fmt.Sprintf("%s#%d", match.Name, match.ID))
		}
		return 0, fmt.Errorf("pane %q is ambiguous (matches: %s)", ref, strings.Join(parts, ", "))
	}
}
