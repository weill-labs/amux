package server

import (
	"net"
	"testing"
	"time"
)

type testClientWriterCommand struct {
	handled chan struct{}
	ret     bool
}

func (c testClientWriterCommand) handle(*clientWriterState, net.Conn) bool {
	if c.handled != nil {
		close(c.handled)
	}
	return c.ret
}

func TestClientWriterLoopSkipsNilCommands(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		commands: make(chan clientWriterCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go w.loop()

	var cmd clientWriterCommand
	w.commands <- cmd
	w.commands <- testClientWriterCommand{handled: handled}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not continue after a nil command")
	}

	w.requestStop()

	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not exit after stop")
	}
}

func TestClientWriterLoopStopsWhenCommandRequestsExit(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		commands: make(chan clientWriterCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go w.loop()

	w.commands <- testClientWriterCommand{handled: handled, ret: true}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("clientWriter command was not handled")
	}

	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not exit after command requested stop")
	}
}

func TestClientWriterEnqueueReturnsFalseWhenStoppedOrDone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		closeStop bool
		closeDone bool
	}{
		{name: "stopped", closeStop: true},
		{name: "done", closeDone: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &clientWriter{
				commands: make(chan clientWriterCommand, 1),
				stop:     make(chan struct{}),
				done:     make(chan struct{}),
			}
			if tt.closeStop {
				close(w.stop)
			}
			if tt.closeDone {
				close(w.done)
			}

			if w.enqueue(testClientWriterCommand{}) {
				t.Fatal("enqueue() = true, want false")
			}
			if w.enqueueAsync(testClientWriterCommand{}) {
				t.Fatal("enqueueAsync() = true, want false")
			}
		})
	}
}

func TestClientWriterSendPaneOutputDropsSlowClientWhenQueueFull(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { peerConn.Close() })

	w := &clientWriter{
		conn:     serverConn,
		commands: make(chan clientWriterCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	w.commands <- testClientWriterCommand{}

	w.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte("x")}, 1, 1)

	select {
	case <-w.stop:
	case <-time.After(time.Second):
		t.Fatal("sendPaneOutput did not stop a slow client")
	}

	_, err := peerConn.Write([]byte("x"))
	if err == nil {
		t.Fatal("client connection remained open after dropping slow client")
	}
}

func TestClientWriterSendBroadcastDropsSlowClientWhenQueueFull(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { peerConn.Close() })

	w := &clientWriter{
		conn:     serverConn,
		commands: make(chan clientWriterCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	w.commands <- testClientWriterCommand{}

	w.sendBroadcast(&Message{Type: MsgTypeLayout})

	select {
	case <-w.stop:
	case <-time.After(time.Second):
		t.Fatal("sendBroadcast did not stop a slow client")
	}

	_, err := peerConn.Write([]byte("x"))
	if err == nil {
		t.Fatal("client connection remained open after dropping slow client")
	}
}

func TestClientWriterSendBroadcastSyncDropsSlowClientWhenQueueFull(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { peerConn.Close() })

	w := &clientWriter{
		conn:     serverConn,
		commands: make(chan clientWriterCommand, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	w.commands <- testClientWriterCommand{}

	w.sendBroadcastSync(&Message{Type: MsgTypeServerReload})

	select {
	case <-w.stop:
	case <-time.After(time.Second):
		t.Fatal("sendBroadcastSync did not stop a slow client")
	}

	_, err := peerConn.Write([]byte("x"))
	if err == nil {
		t.Fatal("client connection remained open after dropping slow client")
	}
}

func TestClientWriterBroadcastCommandHandleNilReply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       clientWriterState
		conn        net.Conn
		wantReturn  bool
		wantClosed  bool
		wantPending int
	}{
		{
			name:       "closed",
			state:      clientWriterState{closed: true, minOutputSeq: make(map[uint32]uint64)},
			wantReturn: true,
			wantClosed: true,
		},
		{
			name:        "bootstrapping",
			state:       clientWriterState{bootstrapping: true, minOutputSeq: make(map[uint32]uint64)},
			wantReturn:  false,
			wantClosed:  false,
			wantPending: 1,
		},
		{
			name:       "ready",
			state:      clientWriterState{minOutputSeq: make(map[uint32]uint64)},
			conn:       discardConn{},
			wantReturn: false,
			wantClosed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := clientWriterBroadcastCommand{msg: &Message{Type: MsgTypeLayout}}
			got := cmd.handle(&tt.state, tt.conn)

			if got != tt.wantReturn {
				t.Fatalf("handle() = %v, want %v", got, tt.wantReturn)
			}
			if tt.state.closed != tt.wantClosed {
				t.Fatalf("state.closed = %v, want %v", tt.state.closed, tt.wantClosed)
			}
			if len(tt.state.pendingMessages) != tt.wantPending {
				t.Fatalf("len(state.pendingMessages) = %d, want %d", len(tt.state.pendingMessages), tt.wantPending)
			}
		})
	}
}

func TestClientWriterBroadcastCommandSignalsReply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		state      clientWriterState
		conn       net.Conn
		wantReturn bool
	}{
		{
			name:       "closed",
			state:      clientWriterState{closed: true, minOutputSeq: make(map[uint32]uint64)},
			wantReturn: true,
		},
		{
			name:       "bootstrapping",
			state:      clientWriterState{bootstrapping: true, minOutputSeq: make(map[uint32]uint64)},
			wantReturn: false,
		},
		{
			name:       "ready",
			state:      clientWriterState{minOutputSeq: make(map[uint32]uint64)},
			conn:       discardConn{},
			wantReturn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reply := make(chan struct{}, 1)
			cmd := clientWriterBroadcastCommand{
				msg:   &Message{Type: MsgTypeLayout},
				reply: reply,
			}

			if got := cmd.handle(&tt.state, tt.conn); got != tt.wantReturn {
				t.Fatalf("handle() = %v, want %v", got, tt.wantReturn)
			}

			select {
			case <-reply:
			case <-time.After(time.Second):
				t.Fatal("handle() did not signal reply")
			}
		})
	}
}

func TestClientWriterNilHelpersAreNoops(t *testing.T) {
	t.Parallel()

	var w *clientWriter
	w.forceCloseConn()
	w.requestStop()
	w.dropSlowClient()
}
