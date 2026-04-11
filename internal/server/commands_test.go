package server

import (
	"testing"
	"time"
)

func TestReplyCommandMutationFlushesCmdResultBeforeExit(t *testing.T) {
	t.Parallel()

	sess := newSession("test-reply-command-mutation-flushes-cmd-result-before-exit")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	conn := newBlockingWriteConn()
	cc := newClientConn(conn)
	t.Cleanup(cc.Close)
	sess.ensureClientManager().setClientsForTest(cc)

	ctx := &CommandContext{CC: cc, Sess: sess}
	done := make(chan struct{})
	go func() {
		ctx.replyCommandMutation(commandMutationResult{
			output:   "Killed pane-1 (session exiting)\n",
			sendExit: true,
		})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("replyCommandMutation returned before the command result write flushed")
	case <-time.After(100 * time.Millisecond):
	}

	close(conn.release)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replyCommandMutation did not return after the command result write was released")
	}

	select {
	case <-conn.writeSeen:
	case <-time.After(time.Second):
		t.Fatal("expected the client writer to attempt a command-result write")
	}
}

func TestCommandStreamSenderFlushWithoutClientIsNoop(t *testing.T) {
	t.Parallel()

	if err := (commandStreamSender{}).Flush(); err != nil {
		t.Fatalf("Flush() error = %v, want nil", err)
	}
}

func TestCommandStreamSenderFlushDelegatesToClient(t *testing.T) {
	t.Parallel()

	conn := newBlockingWriteConn()
	cc := newClientConn(conn)
	t.Cleanup(cc.Close)

	if err := cc.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: "ok\n"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- (commandStreamSender{cc: cc}).Flush()
	}()

	select {
	case err := <-done:
		t.Fatalf("Flush() returned early with %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(conn.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Flush() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Flush() did not return after the client write was released")
	}

	select {
	case <-conn.writeSeen:
	case <-time.After(time.Second):
		t.Fatal("expected Flush() to wait for the client write")
	}
}
