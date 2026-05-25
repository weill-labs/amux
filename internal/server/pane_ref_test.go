package server

import (
	"testing"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

type hostNamesPaneTransport struct {
	stubPaneTransport
	names                  []string
	allHostStatusCallCount int
}

func (t *hostNamesPaneTransport) RemoteHostNames() []string {
	return append([]string(nil), t.names...)
}

func (t *hostNamesPaneTransport) AllHostStatus() map[string]proto.ConnState {
	t.allHostStatusCallCount++
	return t.stubPaneTransport.AllHostStatus()
}

func TestQueryPaneRefResolvesKnownHostOnlyRef(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	installTestPaneTransport(t, sess, &stubPaneTransport{
		hostStatusByName: map[string]proto.ConnState{"builder": proto.Connected},
	}, nil)

	got, err := sess.queryPaneRef("builder")
	if err != nil {
		t.Fatalf("queryPaneRef(builder) error = %v", err)
	}
	if got != (proto.PaneRef{Host: "builder"}) {
		t.Fatalf("queryPaneRef(builder) = %#v, want host-only ref", got)
	}
}

func TestQueryPaneRefUsesRemoteHostNamesWithoutStatusQuery(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	transport := &hostNamesPaneTransport{names: []string{"builder"}}
	installTestPaneTransport(t, sess, transport, nil)

	got, err := sess.queryPaneRef("builder")
	if err != nil {
		t.Fatalf("queryPaneRef(builder) error = %v", err)
	}
	if got != (proto.PaneRef{Host: "builder"}) {
		t.Fatalf("queryPaneRef(builder) = %#v, want host-only ref", got)
	}
	if transport.allHostStatusCallCount != 0 {
		t.Fatalf("AllHostStatus calls = %d, want 0", transport.allHostStatusCallCount)
	}
}

func TestQueryPaneRefRejectsHostPaneNameCollision(t *testing.T) {
	t.Parallel()

	_, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "builder")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)
	installTestPaneTransport(t, sess, &stubPaneTransport{
		hostStatusByName: map[string]proto.ConnState{"builder": proto.Connected},
	}, nil)

	_, err := sess.queryPaneRef("builder")
	if err == nil {
		t.Fatal("queryPaneRef(builder) error = nil, want ambiguity error")
	}
	if got := err.Error(); got != `ambiguous pane ref "builder": matches both a remote host and a local pane; use host/pane or rename the local pane` {
		t.Fatalf("queryPaneRef(builder) error = %q", got)
	}
}

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
