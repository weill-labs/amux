package server

import (
	"fmt"
	"sync/atomic"
	"time"
)

// DefaultVTIdleSettle is the default settle window for VT idle tracking.
const DefaultVTIdleSettle = 2 * time.Second

// DefaultVTIdleTimeout is the default timeout for wait-vt-idle.
const DefaultVTIdleTimeout = 60 * time.Second

// VTIdleTracker tracks per-pane VT output quiescence.
// Event-loop only.
type VTIdleTracker struct {
	clock      Clock
	timers     map[uint32]Timer
	lastOutput map[uint32]time.Time
	settled    map[uint32]bool
	snap       atomic.Pointer[vtIdleSnapshot]
}

type vtIdleSnapshot struct {
	lastOutput map[uint32]time.Time
}

func NewVTIdleTracker(clock Clock) *VTIdleTracker {
	t := &VTIdleTracker{
		clock:      clock,
		timers:     make(map[uint32]Timer),
		lastOutput: make(map[uint32]time.Time),
		settled:    make(map[uint32]bool),
	}
	t.publish()
	return t
}

// TrackOutput records fresh VT output and schedules a vt-idle callback for the
// settle window.
func (t *VTIdleTracker) TrackOutput(paneID uint32, settle time.Duration, onSettled func(time.Time)) {
	now := t.clock.Now()
	t.lastOutput[paneID] = now
	t.settled[paneID] = false
	t.publish()

	if timer := t.timers[paneID]; timer != nil {
		timer.Stop()
	}

	expected := now
	t.timers[paneID] = t.clock.AfterFunc(settle, func() {
		onSettled(expected)
	})
}

// MarkSettled transitions the pane into vt-idle if the timer still matches the
// most recent output edge. Stale callbacks return false.
func (t *VTIdleTracker) MarkSettled(paneID uint32, expected time.Time) bool {
	last, ok := t.lastOutput[paneID]
	if !ok || !last.Equal(expected) || t.settled[paneID] {
		return false
	}
	t.settled[paneID] = true
	return true
}

func (t *VTIdleTracker) LastOutput(paneID uint32) (time.Time, bool) {
	last, ok := t.loadSnapshot().lastOutput[paneID]
	return last, ok
}

func (t *VTIdleTracker) Remaining(paneID uint32, createdAt time.Time, settle time.Duration, now time.Time) time.Duration {
	base := createdAt
	if last, ok := t.LastOutput(paneID); ok {
		base = last
	}
	deadline := base.Add(settle)
	if deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}

func (t *VTIdleTracker) IsSettled(paneID uint32, createdAt time.Time, settle time.Duration, now time.Time) bool {
	return t.Remaining(paneID, createdAt, settle, now) == 0
}

func (t *VTIdleTracker) StopTimer(paneID uint32) {
	if timer := t.timers[paneID]; timer != nil {
		timer.Stop()
		delete(t.timers, paneID)
	}
	delete(t.lastOutput, paneID)
	delete(t.settled, paneID)
	t.publish()
}

func (t *VTIdleTracker) loadSnapshot() *vtIdleSnapshot {
	if snap := t.snap.Load(); snap != nil {
		return snap
	}
	return &vtIdleSnapshot{lastOutput: map[uint32]time.Time{}}
}

func (t *VTIdleTracker) publish() {
	lastOutput := make(map[uint32]time.Time, len(t.lastOutput))
	for paneID, ts := range t.lastOutput {
		lastOutput[paneID] = ts
	}
	t.snap.Store(&vtIdleSnapshot{lastOutput: lastOutput})
}

type paneVTIdleStatus struct {
	idle          bool
	idleSince     time.Time
	lastOutput    time.Time
	hasLastOutput bool
}

func (s *Session) paneVTIdleStatus(paneID uint32, createdAt, now time.Time) paneVTIdleStatus {
	base := createdAt
	status := paneVTIdleStatus{}
	if s.vtIdle != nil {
		if lastOutput, ok := s.vtIdle.LastOutput(paneID); ok {
			status.lastOutput = lastOutput
			status.hasLastOutput = true
			base = lastOutput
		}
	}
	idleSince := base.Add(s.vtIdleSettle())
	if !idleSince.After(now) {
		status.idle = true
		status.idleSince = idleSince
	}
	return status
}

func (s paneVTIdleStatus) listDisplay(now, createdAt time.Time) string {
	if !s.idle {
		return "--"
	}
	base := createdAt
	if s.hasLastOutput {
		base = s.lastOutput
	}
	if base.After(now) {
		return "0s ago"
	}
	return fmt.Sprintf("%ds ago", int(now.Sub(base)/time.Second))
}

type vtIdleWaitState struct {
	createdAt     time.Time
	lastOutput    time.Time
	hasLastOutput bool
	exists        bool
}

func (s *Session) queryVTIdleWaitState(paneID uint32) (vtIdleWaitState, error) {
	return enqueueSessionQuery(s, func(sess *Session) (vtIdleWaitState, error) {
		pane := sess.findPaneByID(paneID)
		if pane == nil {
			return vtIdleWaitState{}, nil
		}
		var lastOutput time.Time
		hasLastOutput := false
		if sess.vtIdle != nil {
			lastOutput, hasLastOutput = sess.vtIdle.LastOutput(paneID)
		}
		return vtIdleWaitState{
			createdAt:     pane.CreatedAt(),
			lastOutput:    lastOutput,
			hasLastOutput: hasLastOutput,
			exists:        true,
		}, nil
	})
}

func (s vtIdleWaitState) remaining(settle time.Duration, now time.Time) time.Duration {
	base := s.createdAt
	if s.hasLastOutput {
		base = s.lastOutput
	}
	deadline := base.Add(settle)
	if deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}
