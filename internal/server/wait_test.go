package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestWaitGeneration_AlreadyPast(t *testing.T) {
	t.Parallel()
	sess := newSession("test-wait-generation-past")
	defer stopSessionBackgroundLoops(t, sess)
	sess.generation.Store(5)

	gen, ok := sess.waitGeneration(3, time.Second)
	if !ok {
		t.Fatal("expected ok=true when generation is already past target")
	}
	if gen != 5 {
		t.Fatalf("expected generation 5, got %d", gen)
	}
}

func TestWaitGeneration_WakesOnIncrement(t *testing.T) {
	t.Parallel()
	sess := newSession("test-wait-generation-increment")
	defer stopSessionBackgroundLoops(t, sess)

	done := make(chan struct{})
	var result uint64
	var resultOk bool
	go func() {
		result, resultOk = sess.waitGeneration(0, 5*time.Second)
		close(done)
	}()

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return len(sess.layoutWaiters) == 1
		})
	})

	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		gen := s.generation.Add(1)
		s.notifyLayoutWaiters(gen)
		return commandMutationResult{}
	})

	select {
	case <-done:
		if !resultOk {
			t.Fatal("expected ok=true")
		}
		if result != 1 {
			t.Fatalf("expected generation 1, got %d", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitGeneration did not return after increment")
	}
}

func TestWaitGeneration_Timeout(t *testing.T) {
	t.Parallel()
	sess := newSession("test-wait-generation-timeout")
	defer stopSessionBackgroundLoops(t, sess)

	gen, ok := sess.waitGeneration(0, 100*time.Millisecond)
	if ok {
		t.Fatal("expected ok=false on timeout")
	}
	if gen != 0 {
		t.Fatalf("expected generation 0, got %d", gen)
	}
}

func TestNotifyPaneOutputSubs(t *testing.T) {
	t.Parallel()
	sess := newSession("test-pane-output-subs")
	stopCrashCheckpointLoop(t, sess)

	ch := sess.enqueuePaneOutputSubscribe(1)

	// Notification should be received (routed through event loop).
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.notifyPaneOutputSubs(1)
		return commandMutationResult{}
	})
	select {
	case <-ch:
		// ok
	case <-time.After(time.Second):
		t.Fatal("expected notification on subscriber channel")
	}

	// Notification for a different pane should NOT be received.
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.notifyPaneOutputSubs(2)
		return commandMutationResult{}
	})
	select {
	case <-ch:
		t.Fatal("should not receive notification for different pane")
	case <-time.After(50 * time.Millisecond):
		// ok
	}

	// Unsubscribe, then synchronize via a no-op mutation to ensure it's processed.
	sess.enqueuePaneOutputUnsubscribe(1, ch)
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		return commandMutationResult{}
	})

	// After unsubscribe, notification should NOT be received.
	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.notifyPaneOutputSubs(1)
		return commandMutationResult{}
	})
	select {
	case <-ch:
		t.Fatal("should not receive notification after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// ok
	}
}

func TestWaitBusyForegroundPID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status mux.AgentStatus
		want   int
	}{
		{
			name:   "idle",
			status: mux.AgentStatus{Idle: true, ChildPIDs: []int{12}},
			want:   0,
		},
		{
			name:   "no children",
			status: mux.AgentStatus{Idle: false, ChildPIDs: nil},
			want:   0,
		},
		{
			name:   "last child is foreground",
			status: mux.AgentStatus{ChildPIDs: []int{12, 34, 56}},
			want:   56,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := waitBusyForegroundPID(tt.status); got != tt.want {
				t.Fatalf("waitBusyForegroundPID(%+v) = %d, want %d", tt.status, got, tt.want)
			}
		})
	}
}

func TestWaitBusyReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		candidatePID int
		status       mux.AgentStatus
		wantNext     int
		wantReady    bool
	}{
		{
			name:         "zero candidate does not satisfy readiness",
			candidatePID: 0,
			status:       mux.AgentStatus{ChildPIDs: []int{91}},
			wantNext:     91,
			wantReady:    false,
		},
		{
			name:         "different foreground child updates candidate",
			candidatePID: 91,
			status:       mux.AgentStatus{ChildPIDs: []int{104}},
			wantNext:     104,
			wantReady:    false,
		},
		{
			name:         "same foreground child is ready",
			candidatePID: 104,
			status:       mux.AgentStatus{ChildPIDs: []int{104}},
			wantNext:     104,
			wantReady:    true,
		},
		{
			name:         "idle clears candidate",
			candidatePID: 104,
			status:       mux.AgentStatus{Idle: true, ChildPIDs: []int{104}},
			wantNext:     0,
			wantReady:    false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotNext, gotReady := waitBusyReady(tt.candidatePID, tt.status)
			if gotNext != tt.wantNext || gotReady != tt.wantReady {
				t.Fatalf("waitBusyReady(%d, %+v) = (%d, %t), want (%d, %t)", tt.candidatePID, tt.status, gotNext, gotReady, tt.wantNext, tt.wantReady)
			}
		})
	}
}
