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

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { serverConn.Close() })
	t.Cleanup(func() { clientConn.Close() })

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

	_, err := clientConn.Write([]byte("x"))
	if err == nil {
		t.Fatal("client connection remained open after dropping slow client")
	}
}

func TestClientWriterNilHelpersAreNoops(t *testing.T) {
	t.Parallel()

	var w *clientWriter
	w.forceCloseConn()
	w.requestStop()
	w.dropSlowClient()
}
