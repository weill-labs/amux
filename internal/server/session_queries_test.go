package server

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestQueryUIClient(t *testing.T) {
	t.Parallel()

	t.Run("no clients", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-none")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		_, err := sess.queryUIClient("", proto.UIEventCopyModeHidden)
		if err == nil || err.Error() != "no client attached" {
			t.Fatalf("queryUIClient error = %v, want no client attached", err)
		}
	})

	t.Run("explicit client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-explicit")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		cc1 := &clientConn{ID: "client-1", inputIdle: true}
		cc2 := &clientConn{ID: "client-2", copyModeShown: true, inputIdle: true}
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(cc1, cc2)
			return struct{}{}
		})

		snap, err := sess.queryUIClient("client-2", proto.UIEventCopyModeShown)
		if err != nil {
			t.Fatalf("queryUIClient explicit: %v", err)
		}
		if snap.client != cc2 {
			t.Fatal("queryUIClient returned the wrong client")
		}
		if snap.clientID != "client-2" {
			t.Fatalf("clientID = %q, want client-2", snap.clientID)
		}
		if !snap.currentMatch {
			t.Fatal("copy-mode-shown should match for client-2")
		}
		if snap.currentGen != 0 {
			t.Fatalf("currentGen = %d, want 0 before any UI transitions", snap.currentGen)
		}
	})

	t.Run("unknown explicit client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-unknown")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(&clientConn{ID: "client-1", inputIdle: true})
			return struct{}{}
		})

		_, err := sess.queryUIClient("missing", proto.UIEventCopyModeHidden)
		if err == nil || err.Error() != "unknown client: missing" {
			t.Fatalf("queryUIClient unknown error = %v, want unknown client", err)
		}
	})

	t.Run("ambiguous without explicit client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-ambiguous")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(
				&clientConn{ID: "client-1", inputIdle: true},
				&clientConn{ID: "client-2", inputIdle: true},
			)
			return struct{}{}
		})

		_, err := sess.queryUIClient("", proto.UIEventCopyModeHidden)
		if err == nil || !strings.Contains(err.Error(), "multiple clients attached; specify --client") {
			t.Fatalf("queryUIClient ambiguous error = %v", err)
		}
		if !strings.Contains(err.Error(), "client-1") || !strings.Contains(err.Error(), "client-2") {
			t.Fatalf("queryUIClient ambiguous error should list both client IDs, got %v", err)
		}
	})

	t.Run("empty event still returns generation", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-generation")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		cc := &clientConn{ID: "client-1", inputIdle: true, uiGeneration: 7}
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(cc)
			return struct{}{}
		})

		snap, err := sess.queryUIClient("", "")
		if err != nil {
			t.Fatalf("queryUIClient generation: %v", err)
		}
		if snap.currentMatch {
			t.Fatal("empty event should not report a current match")
		}
		if snap.currentGen != 7 {
			t.Fatalf("currentGen = %d, want 7", snap.currentGen)
		}
	})
}

func TestResolvePaneAcrossWindowsForActorPrefersActorWindowForDuplicateNames(t *testing.T) {
	t.Parallel()

	sess := newSession("test-resolve-pane-actor-window")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	p1 := newTestPane(sess, 1, "shared")
	p2 := newTestPane(sess, 2, "active")
	p3 := newTestPane(sess, 3, "actor")
	p4 := newTestPane(sess, 4, "shared")

	w1 := newTestWindowWithPanes(t, sess, 1, "window-1", p1, p2)
	w1.FocusPane(p2)
	w2 := newTestWindowWithPanes(t, sess, 2, "window-2", p3, p4)
	w2.FocusPane(p3)

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		sess.Windows = []*mux.Window{w1, w2}
		sess.ActiveWindowID = w1.ID
		sess.Panes = []*mux.Pane{p1, p2, p3, p4}
		return struct{}{}
	})

	resolved, err := enqueueSessionQuery(sess, func(sess *Session) (resolvedPaneRef, error) {
		pane, window, err := sess.resolvePaneAcrossWindowsForActor(p3.ID, "shared")
		if err != nil {
			return resolvedPaneRef{}, err
		}
		return resolvedPaneRef{
			paneID:   pane.ID,
			paneName: pane.Meta.Name,
			windowID: window.ID,
		}, nil
	})
	if err != nil {
		t.Fatalf("resolvePaneAcrossWindowsForActor(shared): %v", err)
	}
	if resolved.paneID != p4.ID || resolved.windowID != w2.ID {
		t.Fatalf("resolvePaneAcrossWindowsForActor(shared) = pane %d window %d, want pane %d window %d", resolved.paneID, resolved.windowID, p4.ID, w2.ID)
	}
}

func TestEnqueueUIWaitSubscribeErrors(t *testing.T) {
	t.Parallel()

	t.Run("no clients", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-ui-wait-subscribe-none")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		_, err := sess.enqueueUIWaitSubscribe("", proto.UIEventCopyModeHidden)
		if err == nil || err.Error() != "no client attached" {
			t.Fatalf("enqueueUIWaitSubscribe error = %v, want no client attached", err)
		}
	})

	t.Run("unknown client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-ui-wait-subscribe-unknown")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.ensureClientManager().setClientsForTest(&clientConn{ID: "client-1", inputIdle: true})
			return struct{}{}
		})

		_, err := sess.enqueueUIWaitSubscribe("missing", proto.UIEventCopyModeHidden)
		if err == nil || err.Error() != "unknown client: missing" {
			t.Fatalf("enqueueUIWaitSubscribe unknown error = %v", err)
		}
	})

	t.Run("session shutdown", func(t *testing.T) {
		t.Parallel()

		sess := &Session{
			sessionEvents:    make(chan sessionEvent, 1),
			sessionEventStop: make(chan struct{}),
			sessionEventDone: make(chan struct{}),
		}

		errCh := make(chan error, 1)
		go func() {
			_, err := sess.enqueueUIWaitSubscribe("", proto.UIEventCopyModeHidden)
			errCh <- err
		}()

		waitUntil(t, func() bool {
			return len(sess.sessionEvents) == 1
		})
		close(sess.sessionEventDone)

		select {
		case err := <-errCh:
			if !errors.Is(err, errSessionShuttingDown) {
				t.Fatalf("enqueueUIWaitSubscribe shutdown error = %v, want %v", err, errSessionShuttingDown)
			}
		case <-time.After(time.Second):
			t.Fatal("enqueueUIWaitSubscribe did not return after shutdown")
		}
	})
}

func TestQueryClientListIncludesCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cc   *clientConn
		want string
	}{
		{
			name: "legacy client",
			cc: &clientConn{
				ID:        "client-1",
				inputIdle: true,
			},
			want: "legacy",
		},
		{
			name: "modern client",
			cc: &clientConn{
				ID:           "client-2",
				inputIdle:    true,
				capabilities: proto.ClientCapabilities{Hyperlinks: true, PromptMarkers: true},
			},
			want: "hyperlinks,prompt_markers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("test-query-client-list-capabilities")
			stopCrashCheckpointLoop(t, sess)
			defer stopSessionBackgroundLoops(t, sess)

			mustSessionQuery(t, sess, func(sess *Session) struct{} {
				sess.ensureClientManager().setClientsForTest(tt.cc)
				return struct{}{}
			})

			clients, err := sess.queryClientList()
			if err != nil {
				t.Fatalf("queryClientList: %v", err)
			}
			if len(clients) != 1 {
				t.Fatalf("len(queryClientList) = %d, want 1", len(clients))
			}
			if got := clients[0].size; got != "0x0" {
				t.Fatalf("size = %q, want 0x0", got)
			}
			if !clients[0].sizeOwner {
				t.Fatal("sizeOwner = false, want true")
			}
			if got := clients[0].capabilities; got != tt.want {
				t.Fatalf("capabilities = %q, want %q", got, tt.want)
			}
		})
	}
}
