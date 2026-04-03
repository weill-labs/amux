package server

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

type createPaneHookTransport struct {
	stubPaneTransport
	onCreatePane func()
}

func (t *createPaneHookTransport) CreatePane(hostName string, localPaneID uint32, sessionName string) (uint32, error) {
	if t.onCreatePane != nil {
		t.onCreatePane()
	}
	return t.stubPaneTransport.CreatePane(hostName, localPaneID, sessionName)
}

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

func TestRunCreatePaneRemoteWindowResolutionFailureDefersClose(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)
	defer cleanup()

	pane := newTestPane(sess, 1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	transport := &createPaneHookTransport{
		onCreatePane: func() {
			mustSessionMutation(t, sess, func(sess *Session) {
				sess.Windows = nil
				sess.ActiveWindowID = 0
			})
		},
	}
	installTestPaneTransport(t, sess, transport, func(string) string { return "123abc" })

	ctx := &CommandContext{Srv: srv, Sess: sess}
	res := runCreatePane(ctx, 0, "spawn", createPanePlacementSplitAt, createPaneRequest{
		hostName:     "dev",
		hostExplicit: true,
		name:         "worker",
		dir:          mux.SplitVertical,
	}, true)

	for _, closePane := range res.ClosePanes {
		closePane := closePane
		t.Cleanup(func() {
			_ = closePane.Close()
			_ = closePane.WaitClosed()
		})
	}

	if res.Err == nil || res.Err.Error() != "pane not in any window" {
		t.Fatalf("runCreatePane error = %v, want pane-not-in-window", res.Err)
	}
	if len(res.ClosePanes) != 1 {
		t.Fatalf("runCreatePane close panes = %d, want 1", len(res.ClosePanes))
	}
	if got := res.ClosePanes[0].Meta.Name; got != "worker" {
		t.Fatalf("prepared pane name = %q, want worker", got)
	}
	if len(transport.removedPanes) != 1 || transport.removedPanes[0] != res.ClosePanes[0].ID {
		t.Fatalf("removed panes = %#v, want [%d]", transport.removedPanes, res.ClosePanes[0].ID)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 1 {
		t.Fatalf("pane count after stale-window failure = %d, want 1", got)
	}
}

func TestRespawnPaneReplaceFailureUsesSessionCloser(t *testing.T) {
	t.Parallel()

	srv, sess, cleanup := newCommandTestSession(t)

	var unblockOnce sync.Once
	unblock := make(chan struct{})
	closeStarted := make(chan struct{})
	closeFinished := make(chan struct{})
	sess.paneCloser = func(pane *mux.Pane) {
		close(closeStarted)
		<-unblock
		pane.Start()
		_ = pane.Close()
		_ = pane.WaitClosed()
		close(closeFinished)
	}
	t.Cleanup(func() {
		unblockOnce.Do(func() { close(unblock) })
		cleanup()
	})

	pane := mustCreatePane(t, sess, srv, 80, 23)
	pane.Start()

	badWindow := newTestWindowWithPanes(t, sess, 99, "other", newTestPane(sess, 2, "pane-2"))
	if _, err := sess.respawnPane(srv, pane, badWindow); err == nil {
		t.Fatal("respawnPane should fail when target window does not contain the pane")
	}

	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("respawnPane failure did not use the session pane closer")
	}

	state := mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount int
		session   *mux.Pane
	} {
		return struct {
			paneCount int
			session   *mux.Pane
		}{
			paneCount: len(sess.Panes),
			session:   sess.findPaneByID(pane.ID),
		}
	})
	if state.paneCount != 1 {
		t.Fatalf("pane count after respawn failure = %d, want 1", state.paneCount)
	}
	if state.session != pane {
		t.Fatal("respawn failure should keep the original pane registered")
	}

	unblockOnce.Do(func() { close(unblock) })
	select {
	case <-closeFinished:
	case <-time.After(time.Second):
		t.Fatal("respawnPane failure did not finish closing the replacement pane")
	}

	state = mustSessionQuery(t, sess, func(sess *Session) struct {
		paneCount int
		session   *mux.Pane
	} {
		return struct {
			paneCount int
			session   *mux.Pane
		}{
			paneCount: len(sess.Panes),
			session:   sess.findPaneByID(pane.ID),
		}
	})
	if state.paneCount != 1 {
		t.Fatalf("pane count after replacement cleanup = %d, want 1", state.paneCount)
	}
	if state.session != pane {
		t.Fatal("replacement cleanup should not remove the original pane")
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
