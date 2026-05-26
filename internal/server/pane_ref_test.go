package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/proto"
)

func TestQueryPaneRefLeavesUnknownNamesLocal(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	got, err := sess.queryPaneRef("pane-1")
	if err != nil {
		t.Fatalf("queryPaneRef(pane-1) error = %v", err)
	}
	if got != (proto.PaneRef{Pane: "pane-1"}) {
		t.Fatalf("queryPaneRef(pane-1) = %#v, want local pane ref", got)
	}
}
