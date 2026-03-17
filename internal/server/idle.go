package server

import (
	"sync"
	"time"
)

// IdleTracker manages per-pane idle timers and state transitions.
//
// Lock ordering: IdleTracker.mu must be acquired before Session.mu
// when both are needed. This prevents deadlocks with trackPaneActivity,
// which acquires idle.mu then calls buildPaneEnv (which needs s.mu).
type IdleTracker struct {
	mu     sync.Mutex
	timers map[uint32]*time.Timer
	state  map[uint32]bool
	since  map[uint32]time.Time
}

// NewIdleTracker creates an IdleTracker with initialized maps.
func NewIdleTracker() *IdleTracker {
	return &IdleTracker{
		timers: make(map[uint32]*time.Timer),
		state:  make(map[uint32]bool),
		since:  make(map[uint32]time.Time),
	}
}

// IsIdle returns whether the pane is currently idle.
func (t *IdleTracker) IsIdle(id uint32) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state[id]
}

// SnapshotState returns a copy of the idle state map.
func (t *IdleTracker) SnapshotState() map[uint32]bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := make(map[uint32]bool, len(t.state))
	for id, idle := range t.state {
		snap[id] = idle
	}
	return snap
}

// SnapshotFull returns copies of both state and since maps.
func (t *IdleTracker) SnapshotFull() (map[uint32]bool, map[uint32]time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	stateSnap := make(map[uint32]bool, len(t.state))
	sinceSnap := make(map[uint32]time.Time, len(t.since))
	for id, idle := range t.state {
		stateSnap[id] = idle
	}
	for id, ts := range t.since {
		sinceSnap[id] = ts
	}
	return stateSnap, sinceSnap
}

// TrackActivity handles a PTY output event for a pane. If the pane was
// idle, it transitions to busy and returns true. The idle timer is reset
// (or created). The onIdle callback fires asynchronously when the pane
// has been quiet for the given timeout.
func (t *IdleTracker) TrackActivity(paneID uint32, timeout time.Duration, onIdle func()) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	wasIdle := t.state[paneID]
	if wasIdle {
		t.state[paneID] = false
		delete(t.since, paneID)
	}

	if timer, ok := t.timers[paneID]; ok {
		timer.Reset(timeout)
	} else {
		t.timers[paneID] = time.AfterFunc(timeout, onIdle)
	}

	return wasIdle
}

// MarkIdle sets a pane as idle with the current timestamp.
func (t *IdleTracker) MarkIdle(paneID uint32) {
	t.mu.Lock()
	t.state[paneID] = true
	t.since[paneID] = time.Now()
	t.mu.Unlock()
}

// StopTimer cleans up the idle timer and state for a closed pane.
func (t *IdleTracker) StopTimer(paneID uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if timer, ok := t.timers[paneID]; ok {
		timer.Stop()
		delete(t.timers, paneID)
		delete(t.state, paneID)
		delete(t.since, paneID)
	}
}
