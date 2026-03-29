package server

import (
	"fmt"
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

// DefaultVTIdleSettle is the default settle window for screen-quiet tracking.
const DefaultVTIdleSettle = 2 * time.Second

// DefaultVTIdleTimeout is the default timeout for wait idle.
const DefaultVTIdleTimeout = 60 * time.Second

// VTIdleTracker tracks per-pane output quiescence.
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

// TrackOutput records fresh output and schedules a screen-quiet callback for
// the settle window.
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

type paneIdleStatus struct {
	idle          bool
	idleSince     time.Time
	lastOutput    time.Time
	hasLastOutput bool
}

func (s *Session) paneIdleStatus(paneID uint32, createdAt, now time.Time) paneIdleStatus {
	base := createdAt
	status := paneIdleStatus{}
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

func (s paneIdleStatus) listDisplay(now, createdAt time.Time) string {
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

type idleWaitState struct {
	createdAt     time.Time
	lastOutput    time.Time
	hasLastOutput bool
	exists        bool
}

func (s *Session) queryIdleWaitState(paneID uint32) (idleWaitState, error) {
	return enqueueSessionQuery(s, func(sess *Session) (idleWaitState, error) {
		pane := sess.findPaneByID(paneID)
		if pane == nil {
			return idleWaitState{}, nil
		}
		var lastOutput time.Time
		hasLastOutput := false
		if sess.vtIdle != nil {
			lastOutput, hasLastOutput = sess.vtIdle.LastOutput(paneID)
		}
		return idleWaitState{
			createdAt:     pane.CreatedAt(),
			lastOutput:    lastOutput,
			hasLastOutput: hasLastOutput,
			exists:        true,
		}, nil
	})
}

func (s idleWaitState) remaining(settle time.Duration, now time.Time) time.Duration {
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

const waitIdleUsage = "usage: wait idle <pane> [--settle <duration>] [--timeout <duration>]"

type waitIdleOptions struct {
	settle  time.Duration
	timeout time.Duration
}

func parseWaitIdleArgs(args []string) (string, waitIdleOptions, error) {
	if len(args) < 1 {
		return "", waitIdleOptions{}, fmt.Errorf(waitIdleUsage)
	}

	opts := waitIdleOptions{
		settle:  DefaultVTIdleSettle,
		timeout: DefaultVTIdleTimeout,
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--settle":
			if i+1 >= len(args) {
				return "", waitIdleOptions{}, fmt.Errorf("missing value for --settle")
			}
			i++
			settle, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitIdleOptions{}, fmt.Errorf("invalid settle: %s", args[i])
			}
			opts.settle = settle
		case "--timeout":
			if i+1 >= len(args) {
				return "", waitIdleOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitIdleOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.timeout = timeout
		default:
			return "", waitIdleOptions{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	return args[0], opts, nil
}

func resetTimer(timer Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}
	timer.Reset(d)
}

func stopTimer(timer Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}
}

func waitForPaneIdle(sess *Session, paneRef string, paneID uint32, opts waitIdleOptions) error {
	outputCh := sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		return fmt.Errorf("session shutting down")
	}
	defer sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	state, err := sess.queryIdleWaitState(paneID)
	if err != nil {
		return err
	}
	if !state.exists {
		return fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
	}

	clk := sess.clock()
	settleTimer := clk.NewTimer(state.remaining(opts.settle, clk.Now()))
	defer settleTimer.Stop()

	timeoutTimer := clk.NewTimer(opts.timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case <-outputCh:
			resetTimer(settleTimer, opts.settle)
		case <-settleTimer.C():
			state, err := sess.queryIdleWaitState(paneID)
			if err != nil {
				return err
			}
			if !state.exists {
				return fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
			}

			remaining := state.remaining(opts.settle, clk.Now())
			if remaining == 0 {
				return nil
			}
			// This receive drained settleTimer.C, so Reset is safe here.
			settleTimer.Reset(remaining)
		case <-timeoutTimer.C():
			return fmt.Errorf("timeout waiting for %s to become idle", paneRef)
		}
	}
}

func cmdWaitIdle(ctx *CommandContext) {
	paneRef, opts, err := parseWaitIdleArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := waitForPaneIdle(ctx.Sess, paneRef, pane.paneID, opts); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply("idle\n")
}
