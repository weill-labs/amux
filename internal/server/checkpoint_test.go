package server

import (
	"errors"
	"testing"
)

func TestServerReloadReturnsSessionShuttingDownBeforeCheckpoint(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	srv := &Server{
		sessions: map[string]*Session{"default": sess},
	}

	err := srv.Reload("/definitely/missing")
	if !errors.Is(err, errSessionShuttingDown) {
		t.Fatalf("Reload() error = %v, want %v", err, errSessionShuttingDown)
	}
	if sess.shutdown.Load() {
		t.Fatal("Reload() should not mark session shutdown on early query failure")
	}
}
