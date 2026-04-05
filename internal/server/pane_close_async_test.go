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

	closed := make(chan *mux.Pane, 1)
	sess.paneCloser = func(pane *mux.Pane) {
		closed <- pane
		_ = pane.Close()
		_ = pane.WaitClosed()
	}

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

	if res.Err == nil || res.Err.Error() != "pane not in any window" {
		t.Fatalf("runCreatePane error = %v, want pane-not-in-window", res.Err)
	}
	if len(res.ClosePanes) != 0 {
		t.Fatalf("runCreatePane close panes = %d, want 0", len(res.ClosePanes))
	}

	var closedPane *mux.Pane
	select {
	case closedPane = <-closed:
	case <-time.After(time.Second):
		t.Fatal("runCreatePane did not schedule the prepared pane for async close")
	}
	if got := closedPane.Meta.Name; got != "worker" {
		t.Fatalf("prepared pane name = %q, want worker", got)
	}
	if len(transport.removedPanes) != 1 || transport.removedPanes[0] != closedPane.ID {
		t.Fatalf("removed panes = %#v, want [%d]", transport.removedPanes, closedPane.ID)
	}
	if got := mustSessionQuery(t, sess, func(sess *Session) int { return len(sess.Panes) }); got != 1 {
		t.Fatalf("pane count after stale-window failure = %d, want 1", got)
	}
}

func TestRespawnCommandFailureUsesSessionCloser(t *testing.T) {
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
	window := newTestWindowWithPanes(t, sess, 1, "main", pane)
	setSessionLayoutForTest(t, sess, window.ID, []*mux.Window{window}, pane)

	sess.localPaneBuilder = func(req localPaneBuildRequest) (*mux.Pane, error) {
		replacement, err := defaultLocalPaneBuilder(req)
		if err != nil {
			return nil, err
		}
		mustSessionMutation(t, sess, func(sess *Session) {
			sess.Windows = nil
			sess.ActiveWindowID = 0
		})
		return replacement, nil
	}

	res := runTestCommand(t, srv, sess, "respawn", pane.Meta.Name)
	if got := res.cmdErr; got != "pane not in any window" {
		t.Fatalf("respawn cmdErr = %q, want %q", got, "pane not in any window")
	}

	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("respawn command failure did not use the session pane closer")
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
		t.Fatal("respawn command failure did not finish closing the replacement pane")
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

func TestEnqueueCommandMutationSchedulesPaneCloseOffLoop(t *testing.T) {
	t.Parallel()

	sess := newSession("test-enqueue-command-mutation-schedule-close")
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

	resultCh := make(chan commandMutationResult, 1)
	go func() {
		resultCh <- sess.enqueueCommandMutation(func(ctx *MutationContext) commandMutationResult {
			ctx.ScheduleClose(pane)
			return commandMutationResult{err: errors.New("boom")}
		})
	}()

	var res commandMutationResult
	select {
	case res = <-resultCh:
	case <-time.After(time.Second):
		t.Fatal("enqueueCommandMutation did not return")
	}
	if res.err == nil || res.err.Error() != "boom" {
		t.Fatalf("enqueueCommandMutation error = %v, want boom", res.err)
	}

	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("scheduled pane close did not start")
	}

	queryDone := make(chan error, 1)
	go func() {
		_, err := enqueueSessionQuery(sess, func(sess *Session) (struct{}, error) {
			return struct{}{}, nil
		})
		queryDone <- err
	}()

	select {
	case err := <-queryDone:
		if err != nil {
			t.Fatalf("session query while scheduled close blocked: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("session event loop blocked while scheduled close was in progress")
	}

	unblockOnce.Do(func() { close(unblock) })
}
