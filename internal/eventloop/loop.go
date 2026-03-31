package eventloop

import (
	"errors"
	"runtime/debug"
)

var ErrStopped = errors.New("event loop stopped")

type Command[S any] interface {
	Handle(*S)
}

type LoopHook[S any] func(*S) bool

// Run processes commands serially until stop closes, the queue closes, or the
// hook returns false.
func Run[S any](state *S, queue <-chan Command[S], stop <-chan struct{}, done chan<- struct{}, hook LoopHook[S]) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case cmd, ok := <-queue:
			if !ok {
				return
			}
			if cmd != nil {
				cmd.Handle(state)
			}
			if hook != nil && !hook(state) {
				return
			}
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
