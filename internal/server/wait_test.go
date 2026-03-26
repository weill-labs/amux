//go:build !race

package server

import (
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
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
			return sess.waiters.layoutWaiterRegistered(0)
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

func TestWaitCrashCheckpoint_AlreadyPast(t *testing.T) {
	t.Parallel()

	sess := newSession("test-wait-crash-checkpoint-past")
	defer stopSessionBackgroundLoops(t, sess)
	sess.waiters.setCrashCheckpointStateForTest(1, "/tmp/checkpoint-1.json")

	record, ok := sess.waitCrashCheckpoint(0, time.Second)
	if !ok {
		t.Fatal("expected ok=true when checkpoint generation is already past target")
	}
	if record.generation != 1 || record.path != "/tmp/checkpoint-1.json" {
		t.Fatalf("waitCrashCheckpoint() = %+v, want generation=1 path=/tmp/checkpoint-1.json", record)
	}
}

func TestWaitCrashCheckpoint_WakesOnWrite(t *testing.T) {
	t.Parallel()

	sess := newSession("test-wait-crash-checkpoint-write")
	defer stopSessionBackgroundLoops(t, sess)

	done := make(chan struct{})
	var record crashCheckpointRecord
	var ok bool
	go func() {
		record, ok = sess.waitCrashCheckpoint(0, 5*time.Second)
		close(done)
	}()

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return sess.waiters.checkpointWaiterRegistered(0)
		})
	})

	if !sess.enqueueEvent(crashCheckpointWrittenEvent{path: "/tmp/checkpoint-2.json"}) {
		t.Fatal("enqueueEvent(crashCheckpointWrittenEvent) = false")
	}

	select {
	case <-done:
		if !ok {
			t.Fatal("expected ok=true")
		}
		if record.generation != 1 || record.path != "/tmp/checkpoint-2.json" {
			t.Fatalf("waitCrashCheckpoint() = %+v, want generation=1 path=/tmp/checkpoint-2.json", record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitCrashCheckpoint did not return after checkpoint write")
	}
}

func TestWaitCrashCheckpoint_Timeout(t *testing.T) {
	t.Parallel()

	sess := newSession("test-wait-crash-checkpoint-timeout")
	defer stopSessionBackgroundLoops(t, sess)

	record, ok := sess.waitCrashCheckpoint(0, 100*time.Millisecond)
	if ok {
		t.Fatal("expected ok=false on timeout")
	}
	if record.generation != 0 || record.path != "" {
		t.Fatalf("waitCrashCheckpoint() = %+v, want zero record", record)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) bool {
		return sess.waiters.checkpointWaiterRegistered(0)
	}); got {
		t.Fatal("checkpoint waiter should be removed after timeout")
	}
}

func TestWaitGenerationAfterCurrent_WaitsForNextGeneration(t *testing.T) {
	t.Parallel()
	sess := newSession("test-wait-generation-after-current")
	defer stopSessionBackgroundLoops(t, sess)
	sess.generation.Store(5)

	done := make(chan struct{})
	var result uint64
	var resultOk bool
	go func() {
		result, resultOk = sess.waitGenerationAfterCurrent(5 * time.Second)
		close(done)
	}()

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return sess.waiters.layoutWaiterRegistered(5)
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
		if result != 6 {
			t.Fatalf("expected generation 6, got %d", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitGenerationAfterCurrent did not return after increment")
	}
}

func TestWaitClipboardAfterCurrent_WaitsForNextClipboard(t *testing.T) {
	t.Parallel()
	sess := newSession("test-wait-clipboard-after-current")
	defer stopSessionBackgroundLoops(t, sess)
	sess.waiters.setClipboardStateForTest(5, "old")

	done := make(chan struct{})
	var result string
	var resultOk bool
	go func() {
		result, resultOk = sess.waitClipboardAfterCurrent(5 * time.Second)
		close(done)
	}()

	waitUntil(t, func() bool {
		return mustSessionQuery(t, sess, func(sess *Session) bool {
			return sess.waiters.clipboardWaiterRegistered(5)
		})
	})

	sess.enqueueCommandMutation(func(s *Session) commandMutationResult {
		s.waiters.recordClipboard([]byte("new"))
		return commandMutationResult{}
	})

	select {
	case <-done:
		if !resultOk {
			t.Fatal("expected ok=true")
		}
		if result != "new" {
			t.Fatalf("expected payload %q, got %q", "new", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitClipboardAfterCurrent did not return after update")
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

func TestBeginPaneOutputWait(t *testing.T) {
	t.Parallel()

	t.Run("matches existing screen content", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-begin-pane-output-wait-matched")
		defer stopSessionBackgroundLoops(t, sess)

		pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
			return len(data), nil
		})
		sess.Panes = []*mux.Pane{pane}

		pane.FeedOutput([]byte("hello"))
		sess.enqueueCommandMutation(func(s *Session) commandMutationResult { return commandMutationResult{} })

		start, err := sess.beginPaneOutputWait(pane.ID, "hello")
		if err != nil {
			t.Fatalf("beginPaneOutputWait error = %v", err)
		}
		if !start.exists {
			t.Fatal("beginPaneOutputWait reported missing pane")
		}
		defer sess.enqueuePaneOutputUnsubscribe(pane.ID, start.ch)
		if !start.matched {
			t.Fatal("beginPaneOutputWait matched = false, want true")
		}
	})

	t.Run("wakes subscribed waiter on later output", func(t *testing.T) {
		t.Parallel()

		sess := newSession("test-begin-pane-output-wait-notify")
		defer stopSessionBackgroundLoops(t, sess)

		pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: config.AccentColor(0)}, 80, 23, sess.paneOutputCallback(), sess.paneExitCallback(), func(data []byte) (int, error) {
			return len(data), nil
		})
		sess.Panes = []*mux.Pane{pane}

		start, err := sess.beginPaneOutputWait(pane.ID, "hello")
		if err != nil {
			t.Fatalf("beginPaneOutputWait error = %v", err)
		}
		if !start.exists {
			t.Fatal("beginPaneOutputWait reported missing pane")
		}
		defer sess.enqueuePaneOutputUnsubscribe(pane.ID, start.ch)
		if start.matched {
			t.Fatal("beginPaneOutputWait matched = true, want false")
		}

		pane.FeedOutput([]byte("hello"))

		select {
		case <-start.ch:
			if !sess.paneScreenContains(pane.ID, "hello") {
				t.Fatal("paneScreenContains(hello) = false, want true after notification")
			}
		case <-time.After(time.Second):
			t.Fatal("expected pane output notification")
		}
	})
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
