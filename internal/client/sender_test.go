package client

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
)

func TestMessageSenderSendAsyncPreservesOrderingWithLaterSyncSend(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })

	sender := newMessageSender(serverConn)
	t.Cleanup(sender.Close)

	if err := sender.SendAsync(&proto.Message{Type: proto.MsgTypeInput, Input: []byte("first")}); err != nil {
		t.Fatalf("SendAsync(first) = %v", err)
	}
	if err := sender.SendAsync(&proto.Message{Type: proto.MsgTypeInput, Input: []byte("second")}); err != nil {
		t.Fatalf("SendAsync(second) = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- sender.Send(&proto.Message{Type: proto.MsgTypeDetach})
	}()

	msg := readSenderMessageWithTimeout(t, peerConn)
	if msg.Type != proto.MsgTypeInput || string(msg.Input) != "first" {
		t.Fatalf("first message = %+v, want input first", msg)
	}

	msg = readSenderMessageWithTimeout(t, peerConn)
	if msg.Type != proto.MsgTypeInput || string(msg.Input) != "second" {
		t.Fatalf("second message = %+v, want input second", msg)
	}

	msg = readSenderMessageWithTimeout(t, peerConn)
	if msg.Type != proto.MsgTypeDetach {
		t.Fatalf("third message type = %v, want detach", msg.Type)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send(detach) = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync Send did not complete after queued async input drained")
	}
}

func TestMessageSenderSendAsyncClonesInput(t *testing.T) {
	t.Parallel()

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = peerConn.Close() })

	sender := newMessageSender(serverConn)
	t.Cleanup(sender.Close)

	input := []byte("hello")
	if err := sender.SendAsync(&proto.Message{Type: proto.MsgTypeInput, Input: input}); err != nil {
		t.Fatalf("SendAsync(input) = %v", err)
	}
	input[0] = 'j'

	msg := readSenderMessageWithTimeout(t, peerConn)
	if got := string(msg.Input); got != "hello" {
		t.Fatalf("async input = %q, want %q", got, "hello")
	}
}

func TestMessageSenderCloseDoesNotBlockOnBlockedAsyncWrite(t *testing.T) {
	t.Parallel()

	conn := newBlockingSenderConn()
	sender := newMessageSender(conn)

	if err := sender.SendAsync(&proto.Message{Type: proto.MsgTypeInput, Input: []byte("blocked")}); err != nil {
		t.Fatalf("SendAsync(blocked) = %v", err)
	}

	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("async write did not start")
	}

	done := make(chan struct{})
	go func() {
		sender.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on a pending async write")
	}
}

func readSenderMessageWithTimeout(t *testing.T, conn net.Conn) *proto.Message {
	t.Helper()

	type readResult struct {
		msg *proto.Message
		err error
	}

	resultCh := make(chan readResult, 1)
	go func() {
		msg, err := proto.ReadMsg(conn)
		resultCh <- readResult{msg: msg, err: err}
	}()

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("ReadMsg: %v", result.err)
		}
		return result.msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sender message")
		return nil
	}
}

type blockingSenderConn struct {
	started chan struct{}
	closed  chan struct{}
}

func newBlockingSenderConn() *blockingSenderConn {
	return &blockingSenderConn{
		started: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (c *blockingSenderConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}

func (c *blockingSenderConn) Write([]byte) (int, error) {
	select {
	case <-c.started:
	default:
		close(c.started)
	}
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockingSenderConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *blockingSenderConn) LocalAddr() net.Addr  { return blockingSenderAddr("local") }
func (c *blockingSenderConn) RemoteAddr() net.Addr { return blockingSenderAddr("remote") }
func (c *blockingSenderConn) SetDeadline(time.Time) error {
	return nil
}
func (c *blockingSenderConn) SetReadDeadline(time.Time) error {
	return nil
}
func (c *blockingSenderConn) SetWriteDeadline(time.Time) error {
	return nil
}

type blockingSenderAddr string

func (a blockingSenderAddr) Network() string { return "test" }
func (a blockingSenderAddr) String() string  { return string(a) }
