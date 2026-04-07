package server

import (
	"net"
	"sync"

	"github.com/weill-labs/amux/internal/proto"
)

const clientWriterQueueSize = 4096

type clientWriterState struct {
	closed          bool
	bootstrapping   bool
	minOutputSeq    map[uint32]uint64
	pendingMessages []pendingMessage
}

type clientWriterCommand interface {
	handle(*clientWriterState, net.Conn, *proto.Writer) bool
}

type clientWriter struct {
	conn         net.Conn
	wire         *proto.Writer
	commands     chan clientWriterCommand
	paneCommands chan clientWriterCommand
	stop         chan struct{}
	done         chan struct{}

	closeOnce sync.Once
	stopOnce  sync.Once
}

type clientWriterMessageCommand struct {
	msg *Message
}

func (c clientWriterMessageCommand) handle(state *clientWriterState, conn net.Conn, wire *proto.Writer) bool {
	_ = writeClientMessage(state, conn, wire, c.msg)
	return state.closed
}

type clientWriterFlushCommand struct {
	reply chan struct{}
}

func (c clientWriterFlushCommand) handle(state *clientWriterState, _ net.Conn, _ *proto.Writer) bool {
	c.reply <- struct{}{}
	return state.closed
}

type clientWriterBroadcastCommand struct {
	msg   *Message
	reply chan struct{}
}

func (c clientWriterBroadcastCommand) handle(state *clientWriterState, conn net.Conn, wire *proto.Writer) bool {
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
	_ = writeClientMessage(state, conn, wire, c.msg)
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

func (c clientWriterPaneOutputCommand) handle(state *clientWriterState, conn net.Conn, wire *proto.Writer) bool {
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
	_ = writeClientMessage(state, conn, wire, c.msg)
	return state.closed
}

type clientWriterPaneMessageCommand struct {
	msg *Message
}

func (c clientWriterPaneMessageCommand) handle(state *clientWriterState, conn net.Conn, wire *proto.Writer) bool {
	if state.closed {
		return true
	}
	if state.bootstrapping {
		state.pendingMessages = append(state.pendingMessages, pendingMessage{msg: cloneMessage(c.msg)})
		return false
	}
	_ = writeClientMessage(state, conn, wire, c.msg)
	return state.closed
}

type clientWriterStartBootstrapCommand struct {
	reply chan struct{}
}

func (c clientWriterStartBootstrapCommand) handle(state *clientWriterState, _ net.Conn, _ *proto.Writer) bool {
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

func (c clientWriterFinishBootstrapCommand) handle(state *clientWriterState, conn net.Conn, wire *proto.Writer) bool {
	if state.closed {
		c.reply <- struct{}{}
		return true
	}

	state.minOutputSeq = cloneMinOutputSeq(c.minOutputSeq)
	for _, pending := range state.pendingMessages {
		if pending.outputSeq != 0 && pending.outputSeq <= state.minOutputSeq[pending.paneID] {
			continue
		}
		if err := writeClientMessage(state, conn, wire, pending.msg); err != nil {
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

func (c clientWriterBootstrappingQuery) handle(state *clientWriterState, _ net.Conn, _ *proto.Writer) bool {
	c.reply <- state.bootstrapping
	return state.closed
}

func newClientWriter(conn net.Conn) *clientWriter {
	if conn == nil {
		return nil
	}
	w := &clientWriter{
		conn:         conn,
		wire:         proto.NewWriter(conn),
		commands:     make(chan clientWriterCommand, clientWriterQueueSize),
		paneCommands: make(chan clientWriterCommand, clientWriterQueueSize),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
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
			case cmd := <-w.commands:
				if cmd == nil {
					continue
				}
				if cmd.handle(&state, w.conn, w.wire) {
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
					if cmd.handle(&state, w.conn, w.wire) {
						return
					}
				case cmd := <-w.paneCommands:
					if cmd == nil {
						continue
					}
					if cmd.handle(&state, w.conn, w.wire) {
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
			if cmd.handle(&state, w.conn, w.wire) {
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
				if cmd.handle(&state, w.conn, w.wire) {
					return
				}
			case cmd := <-w.paneCommands:
				if cmd == nil {
					continue
				}
				if cmd.handle(&state, w.conn, w.wire) {
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
	if !w.enqueue(clientWriterMessageCommand{msg: cloneMessage(msg)}) {
		return net.ErrClosed
	}
	return nil
}

func (w *clientWriter) flush() {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueue(clientWriterFlushCommand{reply: reply}) {
		return
	}
	waitClientWriterAck(w.done, reply)
}

func (w *clientWriter) sendBroadcast(msg *Message) {
	if w == nil {
		return
	}
	w.enqueueAsync(clientWriterBroadcastCommand{msg: msg})
}

func (w *clientWriter) sendBroadcastSync(msg *Message) {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueueAsync(clientWriterBroadcastCommand{msg: msg, reply: reply}) {
		return
	}
	waitClientWriterAck(w.done, reply)
}

func (w *clientWriter) sendPaneOutput(msg *Message, paneID uint32, seq uint64) {
	if w == nil {
		return
	}
	w.enqueueAsyncPane(clientWriterPaneOutputCommand{msg: msg, paneID: paneID, seq: seq})
}

func (w *clientWriter) sendPaneMessage(msg *Message) {
	if w == nil {
		return
	}
	w.enqueueAsyncPane(clientWriterPaneMessageCommand{msg: msg})
}

func (w *clientWriter) startBootstrap() {
	if w == nil {
		return
	}
	reply := make(chan struct{}, 1)
	if !w.enqueue(clientWriterStartBootstrapCommand{reply: reply}) {
		return
	}
	waitClientWriterAck(w.done, reply)
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
	waitClientWriterAck(w.done, reply)
}

func (w *clientWriter) isBootstrapping() bool {
	if w == nil {
		return false
	}
	reply := make(chan bool, 1)
	if !w.enqueue(clientWriterBootstrappingQuery{reply: reply}) {
		return false
	}
	return waitClientWriterBool(w.done, reply, false)
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

func writeClientMessage(state *clientWriterState, conn net.Conn, wire *proto.Writer, msg *Message) error {
	if state.closed || conn == nil {
		return nil
	}
	if err := wire.WriteMsg(msg); err != nil {
		state.closed = true
		_ = conn.Close()
		return err
	}
	return nil
}

func waitClientWriterAck(done <-chan struct{}, reply <-chan struct{}) {
	select {
	case <-reply:
		return
	default:
	}
	select {
	case <-reply:
	case <-done:
		select {
		case <-reply:
		default:
		}
	}
}

func waitClientWriterBool(done <-chan struct{}, reply <-chan bool, fallback bool) bool {
	select {
	case value := <-reply:
		return value
	default:
	}
	select {
	case value := <-reply:
		return value
	case <-done:
		select {
		case value := <-reply:
			return value
		default:
			return fallback
		}
	}
}
