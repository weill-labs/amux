package server

import (
	"net"
	"sync"
)

const clientWriterQueueSize = 256

type clientWriterState struct {
	closed          bool
	bootstrapping   bool
	minOutputSeq    map[uint32]uint64
	pendingMessages []pendingMessage
}

type clientWriterCommand interface {
	handle(*clientWriterState, net.Conn) bool
}

type clientWriter struct {
	conn         net.Conn
	commands     chan clientWriterCommand
	paneCommands chan clientWriterCommand
	stop         chan struct{}
	done         chan struct{}
	onSlowDrop   func()

	closeOnce sync.Once
	stopOnce  sync.Once
}

type clientWriterSendCommand struct {
	msg   *Message
	reply chan error
}

func (c clientWriterSendCommand) handle(state *clientWriterState, conn net.Conn) bool {
	c.reply <- writeClientMessage(state, conn, c.msg)
	return state.closed
}

type clientWriterBroadcastCommand struct {
	msg   *Message
	reply chan struct{}
}

func (c clientWriterBroadcastCommand) handle(state *clientWriterState, conn net.Conn) bool {
	if state.closed {
		if c.reply != nil {
			c.reply <- struct{}{}
		}
		return true
	}
	if state.bootstrapping {
		state.pendingMessages = append(state.pendingMessages, pendingMessage{msg: cloneMessage(c.msg)})
		if c.reply != nil {
			c.reply <- struct{}{}
		}
		return false
	}
	_ = writeClientMessage(state, conn, c.msg)
	if c.reply != nil {
		c.reply <- struct{}{}
	}
	return state.closed
}

type clientWriterPaneOutputCommand struct {
	msg    *Message
	paneID uint32
	seq    uint64
}

func (c clientWriterPaneOutputCommand) handle(state *clientWriterState, conn net.Conn) bool {
	if state.closed {
		return true
	}
	if state.bootstrapping {
		state.pendingMessages = append(state.pendingMessages, pendingMessage{
			msg:       cloneMessage(c.msg),
			paneID:    c.paneID,
			outputSeq: c.seq,
		})
		return false
	}
	if c.seq != 0 && c.seq <= state.minOutputSeq[c.paneID] {
		return false
	}
	_ = writeClientMessage(state, conn, c.msg)
	return state.closed
}

type clientWriterPaneMessageCommand struct {
	msg *Message
}

func (c clientWriterPaneMessageCommand) handle(state *clientWriterState, conn net.Conn) bool {
	if state.closed {
		return true
	}
	if state.bootstrapping {
		state.pendingMessages = append(state.pendingMessages, pendingMessage{msg: cloneMessage(c.msg)})
		return false
	}
	_ = writeClientMessage(state, conn, c.msg)
	return state.closed
}

type clientWriterStartBootstrapCommand struct {
	reply chan struct{}
}

func (c clientWriterStartBootstrapCommand) handle(state *clientWriterState, _ net.Conn) bool {
	if !state.closed {
		state.bootstrapping = true
		state.minOutputSeq = make(map[uint32]uint64)
		state.pendingMessages = nil
	}
	c.reply <- struct{}{}
	return state.closed
}

type clientWriterFinishBootstrapCommand struct {
	minOutputSeq map[uint32]uint64
	reply        chan struct{}
}

func (c clientWriterFinishBootstrapCommand) handle(state *clientWriterState, conn net.Conn) bool {
	if state.closed {
		c.reply <- struct{}{}
		return true
	}

	state.minOutputSeq = cloneMinOutputSeq(c.minOutputSeq)
	for _, pending := range state.pendingMessages {
		if pending.outputSeq != 0 && pending.outputSeq <= state.minOutputSeq[pending.paneID] {
			continue
		}
		if err := writeClientMessage(state, conn, pending.msg); err != nil {
			break
		}
	}
	state.pendingMessages = nil
	state.bootstrapping = false
	c.reply <- struct{}{}
	return state.closed
}

type clientWriterBootstrappingQuery struct {
	reply chan bool
}

func (c clientWriterBootstrappingQuery) handle(state *clientWriterState, _ net.Conn) bool {
	c.reply <- state.bootstrapping
	return state.closed
}

func newClientWriter(conn net.Conn, onSlowDrop func()) *clientWriter {
	if conn == nil {
		return nil
	}
	w := &clientWriter{
		conn:         conn,
		commands:     make(chan clientWriterCommand, clientWriterQueueSize),
		paneCommands: make(chan clientWriterCommand, clientWriterQueueSize),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
		onSlowDrop:   onSlowDrop,
	}
	go w.loop()
	return w
}

func (w *clientWriter) loop() {
	defer close(w.done)

	state := clientWriterState{
		minOutputSeq: make(map[uint32]uint64),
	}
	for {
		select {
		case <-w.stop:
			return
		default:
		}

		if state.bootstrapping {
			select {
			case <-w.stop:
				return
			case cmd := <-w.paneCommands:
				if cmd == nil {
					continue
				}
				if cmd.handle(&state, w.conn) {
					return
				}
			default:
				select {
				case <-w.stop:
					return
				case cmd := <-w.paneCommands:
					if cmd == nil {
						continue
					}
					if cmd.handle(&state, w.conn) {
						return
					}
				case cmd := <-w.commands:
					if cmd == nil {
						continue
					}
					if cmd.handle(&state, w.conn) {
						return
					}
				}
			}
			continue
		}

		select {
		case <-w.stop:
			return
		case cmd := <-w.commands:
			if cmd == nil {
				continue
			}
			if cmd.handle(&state, w.conn) {
				return
			}
		default:
			select {
			case <-w.stop:
				return
			case cmd := <-w.commands:
				if cmd == nil {
					continue
				}
				if cmd.handle(&state, w.conn) {
					return
				}
			case cmd := <-w.paneCommands:
				if cmd == nil {
					continue
				}
				if cmd.handle(&state, w.conn) {
					return
				}
			}
		}
	}
}

func (w *clientWriter) send(msg *Message) error {
	if w == nil {
		return nil
	}
	reply := make(chan error, 1)
	if !w.enqueue(clientWriterSendCommand{msg: msg, reply: reply}) {
		return nil
	}
	return <-reply
}

func (w *clientWriter) sendBroadcast(msg *Message) {
	if w == nil {
		return
	}
	if !w.enqueueAsync(clientWriterBroadcastCommand{msg: msg}) {
		w.dropSlowClient()
		return
	}
}

func (w *clientWriter) sendBroadcastSync(msg *Message) {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueueAsync(clientWriterBroadcastCommand{msg: msg, reply: reply}) {
		w.dropSlowClient()
		return
	}
	<-reply
}

func (w *clientWriter) sendPaneOutput(msg *Message, paneID uint32, seq uint64) {
	if w == nil {
		return
	}
	if !w.enqueueAsyncPane(clientWriterPaneOutputCommand{msg: msg, paneID: paneID, seq: seq}) {
		w.dropSlowClient()
		return
	}
}

func (w *clientWriter) sendPaneMessage(msg *Message) {
	if w == nil {
		return
	}
	if !w.enqueueAsyncPane(clientWriterPaneMessageCommand{msg: msg}) {
		w.dropSlowClient()
		return
	}
}

func (w *clientWriter) startBootstrap() {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueue(clientWriterStartBootstrapCommand{reply: reply}) {
		return
	}
	<-reply
}

func (w *clientWriter) finishBootstrap(minOutputSeq map[uint32]uint64) {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueue(clientWriterFinishBootstrapCommand{
		minOutputSeq: cloneMinOutputSeq(minOutputSeq),
		reply:        reply,
	}) {
		return
	}
	<-reply
}

func (w *clientWriter) isBootstrapping() bool {
	if w == nil {
		return false
	}
	reply := make(chan bool, 1)
	if !w.enqueue(clientWriterBootstrappingQuery{reply: reply}) {
		return false
	}
	return <-reply
}

func (w *clientWriter) close() {
	if w == nil {
		return
	}
	w.forceCloseConn()
	w.requestStop()
	<-w.done
}

func (w *clientWriter) enqueue(cmd clientWriterCommand) bool {
	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	default:
	}

	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	case w.commands <- cmd:
		return true
	}
}

func (w *clientWriter) enqueueAsync(cmd clientWriterCommand) bool {
	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	default:
	}

	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	case w.commands <- cmd:
		return true
	default:
		return false
	}
}

func (w *clientWriter) enqueueAsyncPane(cmd clientWriterCommand) bool {
	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	default:
	}

	select {
	case <-w.stop:
		return false
	case <-w.done:
		return false
	case w.paneCommands <- cmd:
		return true
	default:
		return false
	}
}

func (w *clientWriter) forceCloseConn() {
	if w == nil {
		return
	}
	w.closeOnce.Do(func() {
		if w.conn != nil {
			_ = w.conn.Close()
		}
	})
}

func (w *clientWriter) requestStop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stop) })
}

func (w *clientWriter) dropSlowClient() {
	if w != nil && w.onSlowDrop != nil {
		w.onSlowDrop()
	}
	w.forceCloseConn()
	w.requestStop()
}

func writeClientMessage(state *clientWriterState, conn net.Conn, msg *Message) error {
	if state.closed || conn == nil {
		return nil
	}
	if err := WriteMsg(conn, msg); err != nil {
		state.closed = true
		_ = conn.Close()
		return err
	}
	return nil
}
