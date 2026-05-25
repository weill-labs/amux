package eventloop

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/weill-labs/amux/internal/goroutineid"
)

var ErrStopped = errors.New("event loop stopped")

// DefaultWatchdogTimeout bounds a single Command.Handle call. A wedged actor
// should stop loudly and recover through normal daemon restart instead of
// leaving producers blocked forever.
const DefaultWatchdogTimeout = 30 * time.Second

type Command[S any] interface {
	Handle(context.Context, *S)
}

type QueuedCommand[S any] struct {
	ctx context.Context
	cmd Command[S]
}

type LoopHook[S any] func(*S) bool

type watchdogTimeoutProvider interface {
	EventLoopWatchdogTimeout() time.Duration
}

type watchdogTimeoutHandler interface {
	HandleEventLoopWatchdogTimeout(WatchdogTimeoutInfo)
}

// WatchdogSnapshot is captured on the command handler goroutine before
// Command.Handle starts, then reused by the watchdog goroutine if the handler
// times out.
type WatchdogSnapshot struct {
	StateName string
}

type watchdogSnapshotProvider interface {
	EventLoopWatchdogSnapshot() WatchdogSnapshot
}

// WatchdogTimeoutInfo describes a command handler that exceeded the configured
// watchdog timeout.
type WatchdogTimeoutInfo struct {
	CommandType string
	Started     time.Time
	Elapsed     time.Duration
	Timeout     time.Duration
	GoroutineID uint64
	StateName   string
}

type commandEntryHandler interface {
	EnterEventLoopCommand()
}

type commandTypeNamer interface {
	EventLoopCommandType() string
}

type handleRequest[S any] struct {
	ctx     context.Context
	cmd     Command[S]
	entered chan handleEntry
	done    chan struct{}
}

type handleEntry struct {
	started     time.Time
	goroutineID uint64
	commandType string
	snapshot    WatchdogSnapshot
}

// Run processes commands serially until stop closes, the queue closes, or the
// hook returns false.
func Run[S any](state *S, queue <-chan QueuedCommand[S], stop <-chan struct{}, done chan<- struct{}, hook LoopHook[S]) {
	defer close(done)
	timeout := watchdogTimeout(state)
	if timeout <= 0 {
		runInline(state, queue, stop, hook)
		return
	}

	handlerRequests := make(chan handleRequest[S])
	handlerReady := make(chan struct{})
	go handleCommands(state, handlerRequests, handlerReady)
	<-handlerReady
	defer close(handlerRequests)

	for {
		select {
		case <-stop:
			return
		case cmd, ok := <-queue:
			if !ok {
				return
			}
			if !cmd.ready() {
				continue
			}
			if !handleWithWatchdog(state, handlerRequests, cmd, timeout) {
				return
			}
			if hook != nil && !hook(state) {
				return
			}
		}
	}
}

func runInline[S any](state *S, queue <-chan QueuedCommand[S], stop <-chan struct{}, hook LoopHook[S]) {
	notifyCommandEntry(state)
	for {
		select {
		case <-stop:
			return
		case cmd, ok := <-queue:
			if !ok {
				return
			}
			if cmd.ready() {
				cmd.cmd.Handle(cmd.ctx, state)
			}
			if hook != nil && !hook(state) {
				return
			}
		}
	}
}

func handleCommands[S any](state *S, requests <-chan handleRequest[S], ready chan<- struct{}) {
	notifyCommandEntry(state)
	close(ready)
	for req := range requests {
		req.entered <- handleEntry{
			started:     time.Now(),
			goroutineID: goroutineid.Current(),
			commandType: commandType(req.cmd),
			snapshot:    watchdogSnapshot(state),
		}
		if req.ctx.Err() == nil {
			req.cmd.Handle(req.ctx, state)
		}
		req.done <- struct{}{}
	}
}

func notifyCommandEntry[S any](state *S) {
	if handler, ok := any(state).(commandEntryHandler); ok {
		handler.EnterEventLoopCommand()
	}
}

func handleWithWatchdog[S any](state *S, requests chan<- handleRequest[S], cmd QueuedCommand[S], timeout time.Duration) bool {
	entered := make(chan handleEntry, 1)
	finished := make(chan struct{}, 1)
	requests <- handleRequest[S]{ctx: cmd.ctx, cmd: cmd.cmd, entered: entered, done: finished}
	entry := <-entered

	timer := time.NewTimer(timeout)
	defer stopTimer(timer)

	select {
	case <-finished:
		return true
	case <-timer.C:
		notifyWatchdogTimeout(state, entry, timeout)
		return false
	}
}

func (cmd QueuedCommand[S]) ready() bool {
	if cmd.cmd == nil || cmd.ctx == nil {
		return false
	}
	return cmd.ctx.Err() == nil
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

func notifyWatchdogTimeout[S any](state *S, entry handleEntry, timeout time.Duration) {
	elapsed := time.Since(entry.started)
	if handler, ok := any(state).(watchdogTimeoutHandler); ok {
		handler.HandleEventLoopWatchdogTimeout(WatchdogTimeoutInfo{
			CommandType: entry.commandType,
			Started:     entry.started,
			Elapsed:     elapsed,
			Timeout:     timeout,
			GoroutineID: entry.goroutineID,
			StateName:   entry.snapshot.StateName,
		})
	}
}

func commandType(cmd any) string {
	if namer, ok := cmd.(commandTypeNamer); ok {
		return namer.EventLoopCommandType()
	}
	return fmt.Sprintf("%T", cmd)
}

func watchdogSnapshot[S any](state *S) WatchdogSnapshot {
	if provider, ok := any(state).(watchdogSnapshotProvider); ok {
		return provider.EventLoopWatchdogSnapshot()
	}
	return WatchdogSnapshot{}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func Enqueue[S any](ctx context.Context, queue chan<- QueuedCommand[S], stop <-chan struct{}, cmd Command[S]) bool {
	if ctx == nil {
		panic("eventloop.Enqueue called with nil context")
	}
	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	default:
	}

	select {
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	case queue <- QueuedCommand[S]{ctx: ctx, cmd: cmd}:
		return true
	}
}

type PanicHandler func(any, []byte) error

type QueryResult[T any] struct {
	Value T
	Err   error
}

type QueryCommand[S any, T any] struct {
	Fn      func(context.Context, *S) (T, error)
	Reply   chan QueryResult[T]
	Recover PanicHandler
}

func (q QueryCommand[S, T]) Handle(ctx context.Context, state *S) {
	res := recoverQuery(ctx, q.Fn, state, q.Recover)
	select {
	case q.Reply <- res:
	case <-ctx.Done():
	}
}

func recoverQuery[S any, T any](ctx context.Context, fn func(context.Context, *S) (T, error), state *S, recoverFn PanicHandler) (res QueryResult[T]) {
	defer func() {
		if r := recover(); r != nil {
			if recoverFn != nil {
				res = QueryResult[T]{Err: recoverFn(r, debug.Stack())}
				return
			}
			res = QueryResult[T]{Err: errors.New("internal error")}
		}
	}()
	value, err := fn(ctx, state)
	return QueryResult[T]{Value: value, Err: err}
}

func EnqueueQuery[S any, T any](
	ctx context.Context,
	queue chan<- QueuedCommand[S],
	stop <-chan struct{},
	done <-chan struct{},
	fn func(context.Context, *S) (T, error),
	recoverFn PanicHandler,
	stoppedErr error,
) (T, error) {
	zero := *new(T)

	reply := make(chan QueryResult[T], 1)
	if !Enqueue(ctx, queue, stop, QueryCommand[S, T]{Fn: fn, Reply: reply, Recover: recoverFn}) {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return zero, stoppedErr
	}

	select {
	case res := <-reply:
		return res.Value, res.Err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-done:
		select {
		case res := <-reply:
			return res.Value, res.Err
		default:
			return zero, stoppedErr
		}
	}
}
