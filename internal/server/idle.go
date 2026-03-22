package server

import (
	"sync/atomic"
	"time"
)

// idleTracker manages per-pane idle timers and state transitions.
type idleTracker struct {
	timers map[uint32]*time.Timer
	state  map[uint32]bool
	since  map[uint32]time.Time
	snap   atomic.Pointer[idleSnapshot]
}

type idleSnapshot struct {
	state map[uint32]bool
	since map[uint32]time.Time
}

// newIdleTracker creates an idleTracker with initialized maps.
func newIdleTracker() *idleTracker {
	t := &idleTracker{
		timers: make(map[uint32]*time.Timer),
		state:  make(map[uint32]bool),
		since:  make(map[uint32]time.Time),
	}
	t.publish()
	return t
}

// IsIdle returns whether the pane is currently idle.
func (t *idleTracker) IsIdle(id uint32) bool {
	return t.loadSnapshot().state[id]
}

// SnapshotState returns a copy of the idle state map.
func (t *idleTracker) SnapshotState() map[uint32]bool {
	state := t.loadSnapshot().state
	snap := make(map[uint32]bool, len(state))
	for id, idle := range state {
		snap[id] = idle
	}
	return snap
}

// SnapshotFull returns copies of both state and since maps.
func (t *idleTracker) SnapshotFull() (map[uint32]bool, map[uint32]time.Time) {
	snap := t.loadSnapshot()
	stateSnap := make(map[uint32]bool, len(snap.state))
	sinceSnap := make(map[uint32]time.Time, len(snap.since))
	for id, idle := range snap.state {
		stateSnap[id] = idle
	}
	for id, ts := range snap.since {
		sinceSnap[id] = ts
	}
	return stateSnap, sinceSnap
}

// TrackActivity handles a PTY output event for a pane. If the pane was
// idle, it transitions to busy and returns true. The idle timer is reset
// (or created). The onIdle callback fires asynchronously when the pane
// has been quiet for the given timeout. Event-loop only.
func (t *idleTracker) TrackActivity(paneID uint32, timeout time.Duration, onIdle func()) bool {
	wasIdle := t.state[paneID]
	if wasIdle {
		t.state[paneID] = false
		delete(t.since, paneID)
		t.publish()
	}

	if timer, ok := t.timers[paneID]; ok {
		timer.Reset(timeout)
	} else {
		t.timers[paneID] = time.AfterFunc(timeout, onIdle)
	}

	return wasIdle
}

// MarkIdle sets a pane as idle with the current timestamp. Event-loop only.
func (t *idleTracker) MarkIdle(paneID uint32) {
	t.state[paneID] = true
	t.since[paneID] = time.Now()
	t.publish()
}

// StopTimer cleans up the idle timer and state for a closed pane. Event-loop only.
func (t *idleTracker) StopTimer(paneID uint32) {
	if timer, ok := t.timers[paneID]; ok {
		timer.Stop()
		delete(t.timers, paneID)
	}
	if _, ok := t.state[paneID]; ok {
		delete(t.state, paneID)
		delete(t.since, paneID)
		t.publish()
	}
}

func (t *idleTracker) loadSnapshot() *idleSnapshot {
	if snap := t.snap.Load(); snap != nil {
		return snap
	}
	return &idleSnapshot{
		state: map[uint32]bool{},
		since: map[uint32]time.Time{},
	}
}

func (t *idleTracker) publish() {
	state := make(map[uint32]bool, len(t.state))
	for id, idle := range t.state {
		state[id] = idle
	}
	since := make(map[uint32]time.Time, len(t.since))
	for id, ts := range t.since {
		since[id] = ts
	}
	t.snap.Store(&idleSnapshot{state: state, since: since})
}
