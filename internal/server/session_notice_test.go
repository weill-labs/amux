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
	sess := newSession("test-session-notice-replacement")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	sess.showSessionNotice("first")
	firstToken := uint64(0)
	waitUntil(t, func() bool {
		firstToken = mustSessionQuery(t, sess, func(sess *Session) uint64 {
			if sess.notice != "first" {
				return 0
			}
			return sess.noticeToken
		})
		return firstToken != 0
	})

	sess.showSessionNotice("second")
	secondToken := uint64(0)
	waitUntil(t, func() bool {
		secondToken = mustSessionQuery(t, sess, func(sess *Session) uint64 {
			if sess.notice != "second" {
				return 0
			}
			return sess.noticeToken
		})
		return secondToken != 0 && secondToken != firstToken
	})

	sess.enqueueSessionNoticeClear(firstToken)
	if got := mustSessionQuery(t, sess, func(sess *Session) string { return sess.notice }); got != "second" {
		t.Fatalf("notice after stale clear = %q, want %q", got, "second")
	}

	sess.enqueueSessionNoticeClear(secondToken)
	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) string {
			return sess.notice
		}) == ""
	})
}

func TestShowSessionNoticeIgnoresEmptyMessage(t *testing.T) {
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
