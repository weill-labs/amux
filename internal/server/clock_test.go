package server

import (
	"sync"
	"time"
)

// FakeClock is a test clock where time only advances via Advance().
// Timers fire during Advance() when their deadline is reached.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) newFakeTimer(d time.Duration, fn func()) *fakeTimer {
	// Caller must hold c.mu.
	ft := &fakeTimer{
		clock:    c,
		deadline: c.now.Add(d),
		ch:       make(chan time.Time, 1),
		fn:       fn,
	}
	c.timers = append(c.timers, ft)
	return ft
}

func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.newFakeTimer(d, nil)
}

func (c *FakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.newFakeTimer(d, f)
}

// Advance moves the clock forward by d and fires all expired timers.
// AfterFunc callbacks run synchronously; NewTimer channels receive a value.
//
// Lock order: c.mu is held only to snapshot and update the timer list.
// Individual ft.mu locks are acquired after c.mu is released to avoid
// lock-order inversion with fakeTimer.Reset (which acquires c.mu while
// potentially holding ft.mu from the caller's resetTimer path).
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	snapshot := make([]*fakeTimer, len(c.timers))
	copy(snapshot, c.timers)
	c.mu.Unlock()

	var ready []*fakeTimer
	var remaining []*fakeTimer
	for _, ft := range snapshot {
		ft.mu.Lock()
		stopped := ft.stopped
		pastDeadline := !ft.deadline.After(now)
		ft.mu.Unlock()
		if stopped {
			continue
		}
		if pastDeadline {
			ready = append(ready, ft)
		} else {
			remaining = append(remaining, ft)
		}
	}

	c.mu.Lock()
	c.timers = remaining
	c.mu.Unlock()

	for _, ft := range ready {
		if ft.fn != nil {
			ft.fn()
		} else {
			select {
			case ft.ch <- now:
			default:
			}
		}
	}
}

type fakeTimer struct {
	mu       sync.Mutex
	clock    *FakeClock
	deadline time.Time
	ch       chan time.Time
	fn       func()
	stopped  bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

func (t *fakeTimer) Reset(d time.Duration) bool {
	// Lock order: c.mu first, then t.mu — same order as Advance.
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	t.mu.Lock()
	was := !t.stopped
	t.stopped = false
	t.deadline = t.clock.now.Add(d)
	select {
	case <-t.ch:
	default:
	}
	t.mu.Unlock()

	t.clock.timers = append(t.clock.timers, t)
	return was
}
