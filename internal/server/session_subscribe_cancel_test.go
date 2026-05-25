package server

import (
	"context"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestPaneOutputSubscribeRemovesSubscriberWhenContextCanceledBeforeReply(t *testing.T) {
	t.Parallel()

	sess := newSession("test-pane-output-subscribe-cancel")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		paneOutputSubscribeCmd{
			paneID: 1,
			reply:  make(chan chan struct{}),
		}.handle(ctx, sess)
	}()

	waitUntil(t, func() bool {
		return sess.ensureWaiters().outputSubscriberCount(1) == 1
	})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pane output subscribe handler did not return after context cancellation")
	}

	if got := sess.ensureWaiters().outputSubscriberCount(1); got != 0 {
		t.Fatalf("pane output subscribers after cancellation = %d, want 0", got)
	}
}

func TestEventSubscribeRemovesSubscriberWhenContextCanceledBeforeReply(t *testing.T) {
	t.Parallel()

	sess := newSession("test-event-subscribe-cancel")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		eventSubscribeCmd{
			filter: eventFilter{Types: []string{EventExited}},
			reply:  make(chan eventSubscribeResult),
		}.handle(ctx, sess)
	}()

	waitUntil(t, func() bool {
		return len(sess.eventSubs) == 1
	})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event subscribe handler did not return after context cancellation")
	}

	if got := len(sess.eventSubs); got != 0 {
		t.Fatalf("event subscribers after cancellation = %d, want 0", got)
	}
}

func TestUIWaitSubscribeRemovesSubscriberWhenContextCanceledBeforeReply(t *testing.T) {
	t.Parallel()

	sess := newSession("test-ui-wait-subscribe-cancel")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	cc := newClientConn(nil)
	cc.ID = "client-1"
	cc.inputIdle = true
	defer cc.Close()
	sess.ensureClientManager().setClientsForTest(cc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		uiWaitSubscribeCmd{
			requestedClientID: "client-1",
			eventName:         proto.UIEventInputIdle,
			reply:             make(chan uiWaitSubscribeResult),
		}.handle(ctx, sess)
	}()

	waitUntil(t, func() bool {
		return len(sess.eventSubs) == 1
	})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("UI wait subscribe handler did not return after context cancellation")
	}

	if got := len(sess.eventSubs); got != 0 {
		t.Fatalf("event subscribers after UI wait cancellation = %d, want 0", got)
	}
}
