package server

import (
	"errors"
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

type orderedClientWriterCommand struct {
	label   string
	order   chan<- string
	started chan<- struct{}
	release <-chan struct{}
}

func (c orderedClientWriterCommand) handle(*clientWriterState, net.Conn) bool {
	if c.started != nil {
		close(c.started)
	}
	if c.release != nil {
		<-c.release
	}
	c.order <- c.label
	return false
}

func TestClientWriterLoopSkipsNilCommands(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
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
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
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

func TestClientWriterLoopStopsWhenPaneCommandRequestsExit(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()

	w.paneCommands <- testClientWriterCommand{handled: handled, ret: true}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("clientWriter pane command was not handled")
	}

	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not exit after pane command requested stop")
	}
}

func TestClientWriterBootstrappingSkipsNilPaneCommands(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		conn:         discardConn{},
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()
	defer w.close()

	w.startBootstrap()

	var cmd clientWriterCommand
	w.paneCommands <- cmd
	w.commands <- testClientWriterCommand{handled: handled}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not continue after a nil pane command while bootstrapping")
	}
}

func TestClientWriterBootstrappingStopsWhenPaneCommandRequestsExit(t *testing.T) {
	t.Parallel()

	handled := make(chan struct{})
	w := &clientWriter{
		conn:         discardConn{},
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()

	w.startBootstrap()
	w.paneCommands <- testClientWriterCommand{handled: handled, ret: true}

	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("bootstrapping pane command was not handled")
	}

	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not exit after bootstrapping pane command requested stop")
	}
}

func TestClientWriterBootstrappingStopsOnRequestStop(t *testing.T) {
	t.Parallel()

	w := &clientWriter{
		conn:         discardConn{},
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()

	w.startBootstrap()
	w.requestStop()

	select {
	case <-w.done:
	case <-time.After(time.Second):
		t.Fatal("clientWriter loop did not exit after stop during bootstrap")
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
				commands:     make(chan clientWriterCommand, 1),
				paneCommands: make(chan clientWriterCommand, 1),
				stop:         make(chan struct{}),
				done:         make(chan struct{}),
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
			if w.enqueueAsyncPane(testClientWriterCommand{}) {
				t.Fatal("enqueueAsyncPane() = true, want false")
			}
		})
	}
}

func TestClientWriterSendPaneOutputDropsFrameWhenQueueFull(t *testing.T) {
	t.Parallel()

	w := &clientWriter{
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	w.paneCommands <- testClientWriterCommand{}

	w.sendPaneOutput(&Message{Type: MsgTypePaneOutput, PaneID: 1, PaneData: []byte("x")}, 1, 1)

	select {
	case <-w.stop:
		t.Fatal("sendPaneOutput stopped the writer; want frame drop only")
	default:
	}
}

func TestClientWriterSendBroadcastDropsFrameWhenQueueFull(t *testing.T) {
	t.Parallel()

	w := &clientWriter{
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	w.commands <- testClientWriterCommand{}

	w.sendBroadcast(&Message{Type: MsgTypeLayout})

	select {
	case <-w.stop:
		t.Fatal("sendBroadcast stopped the writer; want frame drop only")
	default:
	}
}

func TestClientWriterSendBroadcastSyncDropsFrameWhenQueueFull(t *testing.T) {
	t.Parallel()

	w := &clientWriter{
		commands:     make(chan clientWriterCommand, 1),
		paneCommands: make(chan clientWriterCommand, 1),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	w.commands <- testClientWriterCommand{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		w.sendBroadcastSync(&Message{Type: MsgTypeServerReload})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sendBroadcastSync blocked instead of dropping frame")
	}

	select {
	case <-w.stop:
		t.Fatal("sendBroadcastSync stopped the writer; want frame drop only")
	default:
	}
}

func TestClientWriterSynchronousHelpersReturnWhenWriterExitsAfterEnqueue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*clientWriter)
	}{
		{
			name: "send",
			run: func(w *clientWriter) {
				errCh := make(chan error, 1)
				go func() {
					errCh <- w.send(&Message{Type: MsgTypeCmdResult})
				}()

				select {
				case <-w.commands:
				case <-time.After(time.Second):
					t.Fatal("send() did not enqueue")
				}

				close(w.done)

				select {
				case err := <-errCh:
					if !errors.Is(err, net.ErrClosed) {
						t.Fatalf("send() error = %v, want %v", err, net.ErrClosed)
					}
				case <-time.After(time.Second):
					t.Fatal("send() did not return after writer exit")
				}
			},
		},
		{
			name: "sendBroadcastSync",
			run: func(w *clientWriter) {
				done := make(chan struct{})
				go func() {
					defer close(done)
					w.sendBroadcastSync(&Message{Type: MsgTypeServerReload})
				}()

				select {
				case <-w.commands:
				case <-time.After(time.Second):
					t.Fatal("sendBroadcastSync() did not enqueue")
				}

				close(w.done)

				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("sendBroadcastSync() did not return after writer exit")
				}
			},
		},
		{
			name: "startBootstrap",
			run: func(w *clientWriter) {
				done := make(chan struct{})
				go func() {
					defer close(done)
					w.startBootstrap()
				}()

				select {
				case <-w.commands:
				case <-time.After(time.Second):
					t.Fatal("startBootstrap() did not enqueue")
				}

				close(w.done)

				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("startBootstrap() did not return after writer exit")
				}
			},
		},
		{
			name: "finishBootstrap",
			run: func(w *clientWriter) {
				done := make(chan struct{})
				go func() {
					defer close(done)
					w.finishBootstrap(map[uint32]uint64{1: 3})
				}()

				select {
				case <-w.commands:
				case <-time.After(time.Second):
					t.Fatal("finishBootstrap() did not enqueue")
				}

				close(w.done)

				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("finishBootstrap() did not return after writer exit")
				}
			},
		},
		{
			name: "isBootstrapping",
			run: func(w *clientWriter) {
				resultCh := make(chan bool, 1)
				go func() {
					resultCh <- w.isBootstrapping()
				}()

				select {
				case <-w.commands:
				case <-time.After(time.Second):
					t.Fatal("isBootstrapping() did not enqueue")
				}

				close(w.done)

				select {
				case got := <-resultCh:
					if got {
						t.Fatal("isBootstrapping() = true after writer exit, want false")
					}
				case <-time.After(time.Second):
					t.Fatal("isBootstrapping() did not return after writer exit")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &clientWriter{
				commands:     make(chan clientWriterCommand, 1),
				paneCommands: make(chan clientWriterCommand, 1),
				stop:         make(chan struct{}),
				done:         make(chan struct{}),
			}

			tt.run(w)
		})
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
}

func TestClientWriterPrioritizesControlMessagesOverPaneOutput(t *testing.T) {
	t.Parallel()

	order := make(chan string, 3)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	w := &clientWriter{
		conn:         discardConn{},
		commands:     make(chan clientWriterCommand, 2),
		paneCommands: make(chan clientWriterCommand, 2),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()
	defer w.close()

	w.paneCommands <- orderedClientWriterCommand{
		label:   "pane-1",
		order:   order,
		started: firstStarted,
		release: releaseFirst,
	}
	w.paneCommands <- orderedClientWriterCommand{
		label: "pane-2",
		order: order,
	}

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first pane output was not handled")
	}

	w.commands <- orderedClientWriterCommand{
		label: "control",
		order: order,
	}
	close(releaseFirst)

	first := readLabelWithTimeout(t, order)
	second := readLabelWithTimeout(t, order)
	third := readLabelWithTimeout(t, order)
	if first != "pane-1" || second != "control" || third != "pane-2" {
		t.Fatalf("handle order = [%s %s %s], want [pane-1 control pane-2]", first, second, third)
	}
}

func TestClientWriterBootstrappingPrioritizesControlMessagesOverPaneOutput(t *testing.T) {
	t.Parallel()

	order := make(chan string, 3)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	w := &clientWriter{
		conn:         discardConn{},
		commands:     make(chan clientWriterCommand, 2),
		paneCommands: make(chan clientWriterCommand, 2),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	go w.loop()
	defer w.close()

	w.startBootstrap()

	w.paneCommands <- orderedClientWriterCommand{
		label:   "pane-1",
		order:   order,
		started: firstStarted,
		release: releaseFirst,
	}
	w.paneCommands <- orderedClientWriterCommand{
		label: "pane-2",
		order: order,
	}

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first pane output was not handled")
	}

	w.commands <- orderedClientWriterCommand{
		label: "control",
		order: order,
	}
	close(releaseFirst)

	first := readLabelWithTimeout(t, order)
	second := readLabelWithTimeout(t, order)
	third := readLabelWithTimeout(t, order)
	if first != "pane-1" || second != "control" || third != "pane-2" {
		t.Fatalf("handle order = [%s %s %s], want [pane-1 control pane-2]", first, second, third)
	}
}

func readLabelWithTimeout(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case label := <-ch:
		return label
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued command")
		return ""
	}
}
