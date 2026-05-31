package remote

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
)

// ResolveWindowErrorKind identifies window-resolution failures that callers can
// handle without string matching.
type ResolveWindowErrorKind string

const (
	ResolveWindowNotFound  ResolveWindowErrorKind = "not_found"
	ResolveWindowAmbiguous ResolveWindowErrorKind = "ambiguous"
)

// ResolveWindowError reports a typed failure to resolve a window reference.
type ResolveWindowError struct {
	Ref     string
	Kind    ResolveWindowErrorKind
	Matches []string // window names that matched, for the ambiguous case
}

func (e *ResolveWindowError) Error() string {
	if e.Kind == ResolveWindowAmbiguous {
		return fmt.Sprintf("window %q is ambiguous (matches: %s)", e.Ref, strings.Join(e.Matches, ", "))
	}
	return fmt.Sprintf("window %q not found", e.Ref)
}

// ResolveWindowFromLayout resolves a window reference against a LayoutSnapshot's
// windows. A purely numeric ref is treated as a 1-based window index; any other
// ref is matched against window names (ambiguous when several share the name).
func ResolveWindowFromLayout(layout *proto.LayoutSnapshot, ref string) (proto.WindowSnapshot, error) {
	notFound := &ResolveWindowError{Ref: ref, Kind: ResolveWindowNotFound}
	if layout == nil || len(layout.Windows) == 0 {
		return proto.WindowSnapshot{}, notFound
	}

	if idx, err := strconv.Atoi(strings.TrimSpace(ref)); err == nil && idx > 0 {
		for _, w := range layout.Windows {
			if w.Index == idx {
				return w, nil
			}
		}
		return proto.WindowSnapshot{}, notFound
	}

	matches := make([]proto.WindowSnapshot, 0) // local accumulator (not a package-level var)
	for _, w := range layout.Windows {
		if w.Name == ref {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return proto.WindowSnapshot{}, notFound
	default:
		names := make([]string, 0, len(matches))
		for _, w := range matches {
			names = append(names, fmt.Sprintf("%s#%d", w.Name, w.ID))
		}
		return proto.WindowSnapshot{}, &ResolveWindowError{Ref: ref, Kind: ResolveWindowAmbiguous, Matches: names}
	}
}
