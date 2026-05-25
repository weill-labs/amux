package eventloop

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
)

var ErrStopped = errors.New("event loop stopped")

const DefaultWatchdogTimeout = 30 * time.Second

type Command[S any] interface {
	Handle(*S)
}

type LoopHook[S any] func(*S) bool

type watchdogTimeoutProvider interface {
	EventLoopWatchdogTimeout() time.Duration
}

type watchdogTimeoutHandler interface {
	HandleEventLoopWatchdogTimeout(commandType string, started time.Time, elapsed, timeout time.Duration, goroutineID uint64)
}

type commandEntryHandler interface {
	EnterEventLoopCommand()
}

type commandTypeNamer interface {
	EventLoopCommandType() string
}

type handleRequest[S any] struct {
	cmd     Command[S]
	entered chan handleEntry
	done    chan struct{}
}

type handleEntry struct {
	started     time.Time
	goroutineID uint64
}

// Run processes commands serially until stop closes, the queue closes, or the
// hook returns false.
func Run[S any](state *S, queue <-chan Command[S], stop <-chan struct{}, done chan<- struct{}, hook LoopHook[S]) {
	defer close(done)
	timeout := watchdogTimeout(state)
	if timeout <= 0 {
		runInline(state, queue, stop, hook)
		return
	}

	handlerRequests := make(chan handleRequest[S])
	go handleCommands(state, handlerRequests)
	defer close(handlerRequests)

	for {
		select {
		case <-stop:
			return
		case cmd, ok := <-queue:
			if !ok {
				return
			}
			if cmd != nil && !handleWithWatchdog(state, handlerRequests, cmd, timeout) {
				return
			}
			if hook != nil && !hook(state) {
				return
			}
		}
	}
}

func runInline[S any](state *S, queue <-chan Command[S], stop <-chan struct{}, hook LoopHook[S]) {
	for {
		select {
		case <-stop:
			return
		case cmd, ok := <-queue:
			if !ok {
				return
			}
			if cmd != nil {
				notifyCommandEntry(state)
				cmd.Handle(state)
			}
			if hook != nil && !hook(state) {
				return
			}
		}
	}
}

func handleCommands[S any](state *S, requests <-chan handleRequest[S]) {
	for req := range requests {
		notifyCommandEntry(state)
		req.entered <- handleEntry{
			started:     time.Now(),
			goroutineID: currentGoroutineID(),
		}
		req.cmd.Handle(state)
		req.done <- struct{}{}
	}
}

func notifyCommandEntry[S any](state *S) {
	if handler, ok := any(state).(commandEntryHandler); ok {
		handler.EnterEventLoopCommand()
	}
}

func handleWithWatchdog[S any](state *S, requests chan<- handleRequest[S], cmd Command[S], timeout time.Duration) bool {
	entered := make(chan handleEntry, 1)
	finished := make(chan struct{}, 1)
	requests <- handleRequest[S]{cmd: cmd, entered: entered, done: finished}
	entry := <-entered

	timer := time.NewTimer(timeout)
	defer stopTimer(timer)

	select {
	case <-finished:
		return true
	case <-timer.C:
		notifyWatchdogTimeout(state, cmd, entry, timeout)
		return false
	}
}

func watchdogTimeout[S any](state *S) time.Duration {
	timeout := DefaultWatchdogTimeout
	if provider, ok := any(state).(watchdogTimeoutProvider); ok {
		configured := provider.EventLoopWatchdogTimeout()
		switch {
		case configured < 0:
			return 0
		case configured > 0:
			timeout = configured
		}
	}
	return timeout
}

func notifyWatchdogTimeout[S any](state *S, cmd Command[S], entry handleEntry, timeout time.Duration) {
	elapsed := time.Since(entry.started)
	if handler, ok := any(state).(watchdogTimeoutHandler); ok {
		handler.HandleEventLoopWatchdogTimeout(commandType(cmd), entry.started, elapsed, timeout, entry.goroutineID)
	}
}

func commandType(cmd any) string {
	if namer, ok := cmd.(commandTypeNamer); ok {
		return namer.EventLoopCommandType()
	}
	return fmt.Sprintf("%T", cmd)
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func Enqueue[S any](queue chan<- Command[S], stop <-chan struct{}, cmd Command[S]) bool {
	select {
	case <-stop:
		return false
	default:
	}

	select {
	case <-stop:
		return false
	case queue <- cmd:
		return true
	}
}

type PanicHandler func(any, []byte) error

type QueryResult[T any] struct {
	Value T
	Err   error
}

type QueryCommand[S any, T any] struct {
	Fn      func(*S) (T, error)
	Reply   chan QueryResult[T]
	Recover PanicHandler
}

func (q QueryCommand[S, T]) Handle(state *S) {
	q.Reply <- recoverQuery(q.Fn, state, q.Recover)
}

func recoverQuery[S any, T any](fn func(*S) (T, error), state *S, recoverFn PanicHandler) (res QueryResult[T]) {
	defer func() {
		if r := recover(); r != nil {
			if recoverFn != nil {
				res = QueryResult[T]{Err: recoverFn(r, debug.Stack())}
				return
			}
			res = QueryResult[T]{Err: errors.New("internal error")}
		}
	}()
	value, err := fn(state)
	return QueryResult[T]{Value: value, Err: err}
}

func EnqueueQuery[S any, T any](
	queue chan<- Command[S],
	stop <-chan struct{},
	done <-chan struct{},
	fn func(*S) (T, error),
	recoverFn PanicHandler,
	stoppedErr error,
) (T, error) {
	zero := *new(T)

	reply := make(chan QueryResult[T], 1)
	if !Enqueue(queue, stop, QueryCommand[S, T]{Fn: fn, Reply: reply, Recover: recoverFn}) {
		return zero, stoppedErr
	}

	select {
	case res := <-reply:
		return res.Value, res.Err
	case <-done:
		select {
		case res := <-reply:
			return res.Value, res.Err
		default:
			return zero, stoppedErr
		}
	}
}
