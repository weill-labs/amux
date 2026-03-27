package server

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

func TestSessionEventLoopStaysResponsiveWhilePaneCloseBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, sess *Session) uint32
		invoke  func(sess *Session, paneID uint32)
		assert  func(*Session) error
	}{
		{
			name: "undo expiry finalizes off loop",
			prepare: func(t *testing.T, sess *Session) uint32 {
				t.Helper()

				pane := newTestPane(sess, 2, "pane-2")
				sess.ensureUndoManager().closedPanes = []closedPaneRecord{{pane: pane}}
				return pane.ID
			},
			invoke: func(sess *Session, paneID uint32) {
				sess.enqueueUndoExpiry(paneID)
			},
			assert: func(sess *Session) error {
				if got := sess.ensureUndoManager().closedPaneCount(); got != 0 {
					return errors.New("undo stack still contains pane being closed")
				}
				return nil
			},
		},
		{
			name: "cleanup timeout finalizes off loop",
			prepare: func(t *testing.T, sess *Session) uint32 {
				t.Helper()

				pane1 := newTestPane(sess, 1, "pane-1")
				pane2 := newTestPane(sess, 2, "pane-2")
				window := newTestWindowWithPanes(t, sess, 1, "main", pane1, pane2)
				sess.Windows = []*mux.Window{window}
				sess.ActiveWindowID = window.ID
				sess.Panes = []*mux.Pane{pane1, pane2}
				return pane2.ID
			},
			invoke: func(sess *Session, paneID uint32) {
				sess.enqueuePaneCleanupTimeout(paneID)
			},
			assert: func(sess *Session) error {
				if got := len(sess.Panes); got != 1 {
					return errors.New("cleanup timeout did not remove pane from session")
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sess := newSession("test-pane-close-async-" + tt.name)
			stopCrashCheckpointLoop(t, sess)

			var unblockOnce sync.Once
			unblock := make(chan struct{})
			closeStarted := make(chan struct{})
			sess.paneCloser = func(pane *mux.Pane) {
				close(closeStarted)
				<-unblock
				_ = pane.Close()
			}
			t.Cleanup(func() {
				unblockOnce.Do(func() { close(unblock) })
				stopSessionBackgroundLoops(t, sess)
			})

			paneID := tt.prepare(t, sess)
			tt.invoke(sess, paneID)

			select {
			case <-closeStarted:
			case <-time.After(time.Second):
				t.Fatal("pane close did not start")
			}

			queryDone := make(chan error, 1)
			go func() {
				_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
					return struct{}{}, tt.assert(sess)
				})
				queryDone <- err
			}()

			select {
			case err := <-queryDone:
				if err != nil {
					t.Fatalf("session query while close blocked: %v", err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("session event loop blocked while pane close was in progress")
			}

			unblockOnce.Do(func() { close(unblock) })
		})
	}
}

func TestReplyCommandMutationSendsErrorWhilePaneCloseBlocks(t *testing.T) {
	t.Parallel()

	sess := newSession("test-reply-command-mutation-close-async")
	stopCrashCheckpointLoop(t, sess)

	var unblockOnce sync.Once
	unblock := make(chan struct{})
	closeStarted := make(chan struct{})
	sess.paneCloser = func(pane *mux.Pane) {
		close(closeStarted)
		<-unblock
		_ = pane.Close()
	}
	t.Cleanup(func() {
		unblockOnce.Do(func() { close(unblock) })
		stopSessionBackgroundLoops(t, sess)
	})

	pane := newTestPane(sess, 1, "pane-1")

	serverConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })

	cc := newClientConn(serverConn)
	t.Cleanup(cc.Close)

	ctx := &CommandContext{CC: cc, Sess: sess}
	go ctx.replyCommandMutation(commandMutationResult{
		err:        errors.New("boom"),
		closePanes: []*mux.Pane{pane},
	})

	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("pane close did not start")
	}

	msg := readMsgWithTimeout(t, peerConn)
	if msg.Type != MsgTypeCmdResult {
		t.Fatalf("message type = %v, want cmd-result", msg.Type)
	}
	if msg.CmdErr != "boom" {
		t.Fatalf("CmdErr = %q, want boom", msg.CmdErr)
	}

	unblockOnce.Do(func() { close(unblock) })
}
