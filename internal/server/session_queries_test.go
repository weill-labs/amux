package server

import (
	"errors"
	"strings"
	"testing"
	"time"

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

		cc1 := &ClientConn{ID: "client-1", inputIdle: true}
		cc2 := &ClientConn{ID: "client-2", copyModeShown: true, inputIdle: true}
		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.clients = []*ClientConn{cc1, cc2}
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
	})

	t.Run("unknown explicit client", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-query-ui-client-unknown")
		stopCrashCheckpointLoop(t, sess)
		defer stopSessionBackgroundLoops(t, sess)

		mustSessionQuery(t, sess, func(sess *Session) struct{} {
			sess.clients = []*ClientConn{{ID: "client-1", inputIdle: true}}
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
			sess.clients = []*ClientConn{
				{ID: "client-1", inputIdle: true},
				{ID: "client-2", inputIdle: true},
			}
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
			sess.clients = []*ClientConn{{ID: "client-1", inputIdle: true}}
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
