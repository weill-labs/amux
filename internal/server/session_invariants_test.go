package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
)

func assertSessionLayoutConsistent(t *testing.T, sess *Session, allowedOrphans ...uint32) {
	t.Helper()

	allowed := make(map[uint32]bool, len(allowedOrphans))
	for _, id := range allowedOrphans {
		allowed[id] = true
	}

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		if len(sess.Windows) > 0 {
			if sess.activeWindow() == nil {
				t.Fatal("active window id does not resolve to a window")
			}
		}

		registry := make(map[uint32]*mux.Pane, len(sess.Panes))
		for _, p := range sess.Panes {
			if registry[p.ID] != nil {
				t.Fatalf("duplicate pane id %d in session registry", p.ID)
			}
			registry[p.ID] = p
		}

		layoutIDs := map[uint32]bool{}
		for _, w := range sess.Windows {
			if w == nil || w.Root == nil {
				t.Fatal("window or root layout is nil")
			}
			if w.ActivePane == nil {
				t.Fatalf("window %q has no active pane", w.Name)
			}
			w.Root.Walk(func(c *mux.LayoutCell) {
				if c == nil || c.Pane == nil {
					return
				}
				if layoutIDs[c.Pane.ID] {
					t.Fatalf("pane %d appears multiple times in window layouts", c.Pane.ID)
				}
				layoutIDs[c.Pane.ID] = true
				if registry[c.Pane.ID] == nil {
					t.Fatalf("pane %d appears in layout but not in session registry", c.Pane.ID)
				}
			})
			if !layoutIDs[w.ActivePane.ID] {
				t.Fatalf("window %q active pane %d is not present in its layout", w.Name, w.ActivePane.ID)
			}
		}

		for _, p := range sess.Panes {
			if layoutIDs[p.ID] || p.Meta.Dormant || allowed[p.ID] {
				continue
			}
			t.Fatalf("pane %d (%s) is registered but not present in any layout", p.ID, p.Meta.Name)
		}
		return struct{}{}
	})
}
