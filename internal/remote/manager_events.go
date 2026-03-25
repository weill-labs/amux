package remote

import "errors"

var errManagerClosed = errors.New("manager closed")

// managerEvent is processed sequentially by the Manager event loop.
// All mutable Manager state is accessed only from within event handlers.
type managerEvent interface {
	handle(*Manager) bool
}

type managerQueryResult[T any] struct {
	value T
	err   error
}

type managerQueryEvent[T any] struct {
	fn    func(*Manager) (T, error)
	reply chan managerQueryResult[T]
}

func (e managerQueryEvent[T]) handle(m *Manager) bool {
	value, err := e.fn(m)
	e.reply <- managerQueryResult[T]{value: value, err: err}
	return false
}

type managerShutdownEvent struct {
	reply chan []*HostConn
}

func (e managerShutdownEvent) handle(m *Manager) bool {
	hosts := make([]*HostConn, 0, len(m.hosts))
	for _, hc := range m.hosts {
		hosts = append(hosts, hc)
	}
	m.hosts = make(map[string]*HostConn)
	m.localToHost = make(map[uint32]string)
	e.reply <- hosts
	return true
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
	m.cmds = make(chan managerEvent)
	m.stop = make(chan struct{})
	m.done = make(chan struct{})
	go m.eventLoop()
}

func (m *Manager) eventLoop() {
	defer close(m.done)
	for {
		ev := <-m.cmds
		if ev == nil {
			continue
		}
		if ev.handle(m) {
			return
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
