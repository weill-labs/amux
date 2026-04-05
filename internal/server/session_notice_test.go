package server

import (
	"testing"
	"time"
)

func TestShowSessionNoticeLifecycle(t *testing.T) {
	t.Setenv("AMUX_NOTICE_DURATION", "40ms")

	sess := newSession("test-session-notice-lifecycle")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	sess.showSessionNotice("takeover badhost: timeout")

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) string {
			return sess.notice
		}) == "takeover badhost: timeout"
	})
	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) string {
			return sess.notice
		}) == ""
	})
}

func TestShowSessionNoticeReplacementIgnoresStaleTimer(t *testing.T) {
	t.Parallel()

	sess := &Session{idle: NewIdleTracker(nil), takenOverPanes: make(map[uint32]bool)}

	firstReply := make(chan sessionNoticeSetResult, 1)
	sessionNoticeSetCmd{message: "first", reply: firstReply}.handle(sess)
	firstToken := (<-firstReply).token

	secondReply := make(chan sessionNoticeSetResult, 1)
	sessionNoticeSetCmd{message: "second", reply: secondReply}.handle(sess)
	secondToken := (<-secondReply).token

	sessionNoticeClearCmd{token: firstToken}.handle(sess)
	if got := sess.notice; got != "second" {
		t.Fatalf("notice after stale clear = %q, want %q", got, "second")
	}

	sessionNoticeClearCmd{token: secondToken}.handle(sess)
	if got := sess.notice; got != "" {
		t.Fatalf("notice after matching clear = %q, want empty", got)
	}
}

func TestShowSessionNoticeIgnoresEmptyMessage(t *testing.T) {
	t.Parallel()

	sess := newSession("test-session-notice-empty")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	before := sess.generation.Load()
	sess.showSessionNotice("")

	if got := mustSessionQuery(t, sess, func(sess *Session) string { return sess.notice }); got != "" {
		t.Fatalf("notice = %q, want empty", got)
	}
	if got := sess.generation.Load(); got != before {
		t.Fatalf("generation = %d, want %d", got, before)
	}
}

func TestSessionNoticeDurationFallback(t *testing.T) {
	t.Setenv("AMUX_NOTICE_DURATION", "bogus")
	if got := sessionNoticeDuration(); got != defaultSessionNoticeDuration {
		t.Fatalf("invalid duration = %v, want %v", got, defaultSessionNoticeDuration)
	}

	t.Setenv("AMUX_NOTICE_DURATION", "0s")
	if got := sessionNoticeDuration(); got != defaultSessionNoticeDuration {
		t.Fatalf("zero duration = %v, want %v", got, defaultSessionNoticeDuration)
	}
}

func TestEnqueueSessionNoticeSetReturnsZeroWhenSessionIsShuttingDown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	if got := sess.enqueueSessionNoticeSet("notice"); got.token != 0 {
		t.Fatalf("token = %d, want 0", got.token)
	}
}

func TestEnqueueSessionNoticeSetReturnsZeroWhenSessionStopsBeforeReply(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}

	resultCh := make(chan sessionNoticeSetResult, 1)
	go func() {
		resultCh <- sess.enqueueSessionNoticeSet("notice")
	}()

	waitUntil(t, func() bool {
		return len(sess.sessionEvents) == 1
	})
	close(sess.sessionEventDone)

	select {
	case got := <-resultCh:
		if got.token != 0 {
			t.Fatalf("token = %d, want 0", got.token)
		}
	case <-time.After(time.Second):
		t.Fatal("enqueueSessionNoticeSet did not return after shutdown")
	}
}

func TestShowSessionNoticeReturnsWhenSessionIsShuttingDown(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
		sessionEventDone: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	sess.showSessionNotice("notice")

	if sess.notice != "" {
		t.Fatalf("notice = %q, want empty", sess.notice)
	}
}
