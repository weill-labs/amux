package server

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestWaitGeneration_AlreadyPast(t *testing.T) {
	t.Parallel()
	sess := &Session{}
	sess.generationCond = sync.NewCond(&sess.generationMu)
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
	sess := &Session{}
	sess.generationCond = sync.NewCond(&sess.generationMu)
	sess.generation.Store(0)

	done := make(chan struct{})
	var result uint64
	var resultOk bool
	ready := make(chan struct{})
	go func() {
		close(ready)
		result, resultOk = sess.waitGeneration(0, 5*time.Second)
		close(done)
	}()

	// Wait for the goroutine to be scheduled. Gosched gives it a chance to
	// enter Wait, but the waitGeneration loop handles the case where the
	// broadcast fires before Wait is reached.
	<-ready
	runtime.Gosched()

	// Simulate broadcastLayout incrementing generation.
	sess.generationMu.Lock()
	sess.generation.Add(1)
	sess.generationCond.Broadcast()
	sess.generationMu.Unlock()

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
	sess := &Session{}
	sess.generationCond = sync.NewCond(&sess.generationMu)
	sess.generation.Store(0)

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
	sess := &Session{}

	ch := sess.subscribePaneOutput(1)

	// Notification should be received.
	sess.notifyPaneOutputSubs(1)
	select {
	case <-ch:
		// ok
	case <-time.After(time.Second):
		t.Fatal("expected notification on subscriber channel")
	}

	// Notification for a different pane should NOT be received.
	sess.notifyPaneOutputSubs(2)
	select {
	case <-ch:
		t.Fatal("should not receive notification for different pane")
	case <-time.After(50 * time.Millisecond):
		// ok
	}

	sess.unsubscribePaneOutput(1, ch)

	// After unsubscribe, notification should NOT be received.
	sess.notifyPaneOutputSubs(1)
	select {
	case <-ch:
		t.Fatal("should not receive notification after unsubscribe")
	case <-time.After(50 * time.Millisecond):
		// ok
	}
}
