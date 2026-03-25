package server

import "time"

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
}

func NewVTIdleTracker() *VTIdleTracker {
	return NewVTIdleTrackerWithClock(RealClock{})
}

func NewVTIdleTrackerWithClock(clock Clock) *VTIdleTracker {
	return &VTIdleTracker{
		clock:      clock,
		timers:     make(map[uint32]Timer),
		lastOutput: make(map[uint32]time.Time),
		settled:    make(map[uint32]bool),
	}
}

// TrackOutput records fresh VT output and schedules a vt-idle callback for the
// settle window.
func (t *VTIdleTracker) TrackOutput(paneID uint32, settle time.Duration, onSettled func(time.Time)) {
	now := t.clock.Now()
	t.lastOutput[paneID] = now
	t.settled[paneID] = false

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
	last, ok := t.lastOutput[paneID]
	return last, ok
}

func (t *VTIdleTracker) Remaining(paneID uint32, createdAt time.Time, settle time.Duration, now time.Time) time.Duration {
	base := createdAt
	if last, ok := t.lastOutput[paneID]; ok {
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
