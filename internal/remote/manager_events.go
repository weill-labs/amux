package remote

import "errors"

var errManagerClosed = errors.New("manager closed")

// managerEvent is processed sequentially by the Manager event loop.
// All mutable Manager state is accessed only from within event handlers.
type managerEvent interface {
	handle(*Manager)
}

type managerQueryResult[T any] struct {
	value T
	err   error
}

type managerQueryEvent[T any] struct {
	fn    func(*Manager) (T, error)
	reply chan managerQueryResult[T]
}

func (e managerQueryEvent[T]) handle(m *Manager) {
	value, err := e.fn(m)
	e.reply <- managerQueryResult[T]{value: value, err: err}
}

func enqueueManagerQuery[T any](m *Manager, fn func(*Manager) (T, error)) (T, error) {
	var zero T

	reply := make(chan managerQueryResult[T], 1)
	if !m.enqueue(managerQueryEvent[T]{fn: fn, reply: reply}) {
		return zero, errManagerClosed
	}

	select {
	case res := <-reply:
		return res.value, res.err
	case <-m.done:
		select {
		case res := <-reply:
			return res.value, res.err
		default:
			return zero, errManagerClosed
		}
	}
}

func (m *Manager) startEventLoop() {
	m.cmds = make(chan managerEvent, 256)
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	go m.eventLoop()
}

func (m *Manager) eventLoop() {
	defer close(m.done)
	for {
		select {
		case <-m.stop:
			return
		case ev := <-m.cmds:
			if ev != nil {
				ev.handle(m)
			}
		}
	}
}

func (m *Manager) enqueue(ev managerEvent) bool {
	if m.closed.Load() {
		return false
	}

	select {
	case <-m.stop:
		return false
	default:
	}

	select {
	case <-m.stop:
		return false
	case m.cmds <- ev:
		return true
	}
}
