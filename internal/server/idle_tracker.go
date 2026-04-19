package server

import (
	"sync/atomic"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
)

// IdleTracker owns the server's per-pane input-idle and vt-idle bookkeeping.
type IdleTracker struct {
	input *inputIdleTracker
	vt    *VTIdleTracker

	VTIdleSettle  time.Duration
	VTIdleTimeout time.Duration
}

func NewIdleTracker(clock func() Clock) *IdleTracker {
	return &IdleTracker{
		input: newInputIdleTracker(clock),
		vt:    newVTIdleTracker(clock),
	}
}

func (t *IdleTracker) Settle() time.Duration {
	if t == nil || t.VTIdleSettle == 0 {
		return config.VTIdleSettle
	}
	return t.VTIdleSettle
}

func (t *IdleTracker) Timeout() time.Duration {
	if t == nil || t.VTIdleTimeout == 0 {
		return config.VTIdleTimeout
	}
	return t.VTIdleTimeout
}

func (t *IdleTracker) IsIdle(id uint32) bool {
	if t == nil || t.input == nil {
		return false
	}
	return t.input.IsIdle(id)
}

func (t *IdleTracker) SnapshotState(panes []*mux.Pane, now time.Time) map[uint32]bool {
	if t == nil || t.input == nil {
		return map[uint32]bool{}
	}

	idleSnap := t.input.SnapshotState()
	if t.vt == nil {
		return idleSnap
	}

	for _, pane := range panes {
		if idleSnap[pane.ID] {
			continue
		}
		if t.vt.IsSettled(pane.ID, pane.CreatedAt(), t.Settle(), now) {
			idleSnap[pane.ID] = true
		}
	}
	return idleSnap
}

func (t *IdleTracker) SnapshotFull() (map[uint32]bool, map[uint32]time.Time) {
	if t == nil || t.input == nil {
		return map[uint32]bool{}, map[uint32]time.Time{}
	}
	return t.input.SnapshotFull()
}

func (t *IdleTracker) TrackOutput(paneID uint32, onIdle func(), onSettled func(time.Time)) bool {
	if t == nil {
		return false
	}
	if t.vt != nil {
		t.vt.TrackOutput(paneID, t.Settle(), onSettled)
	}
	if t.input == nil {
		return false
	}
	return t.input.TrackActivity(paneID, t.Settle(), onIdle)
}

func (t *IdleTracker) HandleIdleTimeout(paneID uint32) {
	if t == nil || t.input == nil {
		return
	}
	t.input.MarkIdle(paneID)
}

func (t *IdleTracker) HandleVTIdleTimeout(paneID uint32, expected time.Time) bool {
	if t == nil || t.vt == nil {
		return false
	}
	return t.vt.MarkSettled(paneID, expected)
}

func (t *IdleTracker) PrimeSettling(paneID uint32, at time.Time) {
	if t == nil || t.vt == nil {
		return
	}
	t.vt.PrimeSettling(paneID, at)
}

func (t *IdleTracker) StopPane(paneID uint32) {
	if t == nil {
		return
	}
	if t.input != nil {
		t.input.StopTimer(paneID)
	}
	if t.vt != nil {
		t.vt.StopTimer(paneID)
	}
}

func (t *IdleTracker) LastOutput(paneID uint32) (time.Time, bool) {
	if t == nil || t.vt == nil {
		return time.Time{}, false
	}
	return t.vt.LastOutput(paneID)
}

func (t *IdleTracker) PaneStatus(paneID uint32, createdAt, now time.Time) paneIdleStatus {
	base := createdAt
	status := paneIdleStatus{}
	if lastOutput, ok := t.LastOutput(paneID); ok {
		status.lastOutput = lastOutput
		status.hasLastOutput = true
		base = lastOutput
	}
	idleSince := base.Add(t.Settle())
	if !idleSince.After(now) {
		status.idle = true
		status.idleSince = idleSince
	}
	return status
}

func (t *IdleTracker) WaitState(paneID uint32, createdAt time.Time) idleWaitState {
	lastOutput, hasLastOutput := t.LastOutput(paneID)
	return idleWaitState{
		createdAt:     createdAt,
		lastOutput:    lastOutput,
		hasLastOutput: hasLastOutput,
		exists:        true,
	}
}

type inputIdleTracker struct {
	clock  func() Clock
	timers map[uint32]Timer
	state  map[uint32]bool
	since  map[uint32]time.Time
	snap   atomic.Pointer[idleSnapshot]
}

type idleSnapshot struct {
	state map[uint32]bool
	since map[uint32]time.Time
}

func newInputIdleTracker(clock func() Clock) *inputIdleTracker {
	t := &inputIdleTracker{
		clock:  normalizeClockFunc(clock),
		timers: make(map[uint32]Timer),
		state:  make(map[uint32]bool),
		since:  make(map[uint32]time.Time),
	}
	t.publish()
	return t
}

func (t *inputIdleTracker) IsIdle(id uint32) bool {
	return t.loadSnapshot().state[id]
}

func (t *inputIdleTracker) SnapshotState() map[uint32]bool {
	state := t.loadSnapshot().state
	snap := make(map[uint32]bool, len(state))
	for id, idle := range state {
		snap[id] = idle
	}
	return snap
}

func (t *inputIdleTracker) SnapshotFull() (map[uint32]bool, map[uint32]time.Time) {
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

func (t *inputIdleTracker) TrackActivity(paneID uint32, timeout time.Duration, onIdle func()) bool {
	wasIdle := t.state[paneID]
	if wasIdle {
		t.state[paneID] = false
		delete(t.since, paneID)
		t.publish()
	}

	if timer := t.timers[paneID]; timer != nil {
		timer.Reset(timeout)
	} else {
		t.timers[paneID] = t.clock().AfterFunc(timeout, onIdle)
	}

	return wasIdle
}

func (t *inputIdleTracker) MarkIdle(paneID uint32) {
	t.state[paneID] = true
	t.since[paneID] = t.clock().Now()
	t.publish()
}

func (t *inputIdleTracker) StopTimer(paneID uint32) {
	if timer := t.timers[paneID]; timer != nil {
		timer.Stop()
		delete(t.timers, paneID)
	}
	if _, ok := t.state[paneID]; ok {
		delete(t.state, paneID)
		delete(t.since, paneID)
		t.publish()
	}
}

func (t *inputIdleTracker) loadSnapshot() *idleSnapshot {
	if snap := t.snap.Load(); snap != nil {
		return snap
	}
	return &idleSnapshot{
		state: map[uint32]bool{},
		since: map[uint32]time.Time{},
	}
}

func (t *inputIdleTracker) publish() {
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

// VTIdleTracker tracks per-pane output quiescence.
// Event-loop only.
type VTIdleTracker struct {
	clock      func() Clock
	timers     map[uint32]Timer
	lastOutput map[uint32]time.Time
	settled    map[uint32]bool
	snap       atomic.Pointer[vtIdleSnapshot]
}

type vtIdleSnapshot struct {
	lastOutput map[uint32]time.Time
}

func newVTIdleTracker(clock func() Clock) *VTIdleTracker {
	t := &VTIdleTracker{
		clock:      normalizeClockFunc(clock),
		timers:     make(map[uint32]Timer),
		lastOutput: make(map[uint32]time.Time),
		settled:    make(map[uint32]bool),
	}
	t.publish()
	return t
}

// TrackOutput records fresh output and schedules a screen-quiet callback for
// the settle window.
func (t *VTIdleTracker) TrackOutput(paneID uint32, settle time.Duration, onSettled func(time.Time)) {
	now := t.clock().Now()
	t.lastOutput[paneID] = now
	t.settled[paneID] = false
	t.publish()

	if timer := t.timers[paneID]; timer != nil {
		timer.Stop()
	}

	expected := now
	t.timers[paneID] = t.clock().AfterFunc(settle, func() {
		onSettled(expected)
	})
}

// PrimeSettling marks a pane as having recent activity without scheduling a
// synthetic settled callback. Restores use this when a fresh runtime replaces
// an older pane but the pane keeps its historical CreatedAt for display.
func (t *VTIdleTracker) PrimeSettling(paneID uint32, at time.Time) {
	if at.IsZero() {
		at = t.clock().Now()
	}
	if timer := t.timers[paneID]; timer != nil {
		timer.Stop()
		delete(t.timers, paneID)
	}
	t.lastOutput[paneID] = at
	t.settled[paneID] = false
	t.publish()
}

// MarkSettled records that the quiet timer still matches the most recent output
// edge. Stale callbacks return false.
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

func normalizeClockFunc(clock func() Clock) func() Clock {
	if clock != nil {
		return clock
	}
	return func() Clock { return RealClock{} }
}
