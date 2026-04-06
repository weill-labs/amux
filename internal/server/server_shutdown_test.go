package server

import (
	"net"
	"testing"
	"time"
)

func TestShutdownWaitsForInFlightOneShotCommands(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	srv.listener = &trackingListener{}
	srv.sockPath = t.TempDir() + "/amux.sock"
	srv.shutdownDone = make(chan struct{})

	started := make(chan struct{})
	release := make(chan struct{})
	srv.commands = map[string]CommandHandler{
		"block": func(ctx *CommandContext) {
			close(started)
			<-release
			ctx.reply("ok\n")
		},
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		srv.handleOneShot(serverConn, &Message{Type: MsgTypeCommand, CmdName: "block"})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("one-shot command did not start")
	}

	replyCh := make(chan *Message, 1)
	readErrCh := make(chan error, 1)
	go func() {
		msg, err := ReadMsg(clientConn)
		if err != nil {
			readErrCh <- err
			return
		}
		replyCh <- msg
	}()

	shutdownDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned before in-flight one-shot command finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case msg := <-replyCh:
		if msg.Type != MsgTypeCmdResult || msg.CmdOutput != "ok\n" {
			t.Fatalf("one-shot reply = %#v, want ok cmd result", msg)
		}
	case err := <-readErrCh:
		t.Fatalf("ReadMsg() error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for one-shot reply")
	}

	select {
	case <-commandDone:
	case <-time.After(time.Second):
		t.Fatal("handleOneShot did not return")
	}

	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not return after one-shot completed")
	}

	if sess.shutdown.Load() != true {
		t.Fatal("session shutdown flag not set")
	}
}
