package mirror

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

func TestManagerDropsStaleGenerationFrames(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}

	mgr.mu.Lock()
	ms := mgr.mirrors[pane.ID]
	ms.state = StateConnected
	ms.generation = 1
	mgr.mu.Unlock()

	if err := mgr.applyMessage(pane.ID, 1, paneOutput("old-frame")); err != nil {
		t.Fatalf("apply old generation: %v", err)
	}
	if !pane.ScreenContains("old-frame") {
		t.Fatal("pane did not apply initial generation frame")
	}

	gen, ok := mgr.markConnected(pane.ID, 42, nil)
	if !ok {
		t.Fatal("markConnected returned ok=false")
	}
	if gen != 2 {
		t.Fatalf("generation = %d, want 2", gen)
	}

	if err := mgr.applyMessage(pane.ID, 1, paneOutput("stale-frame")); err != nil {
		t.Fatalf("apply stale generation: %v", err)
	}
	if err := mgr.applyMessage(pane.ID, 2, paneOutput("current-frame")); err != nil {
		t.Fatalf("apply current generation: %v", err)
	}
	if pane.ScreenContains("stale-frame") {
		t.Fatal("stale generation frame was applied")
	}
	if !pane.ScreenContains("current-frame") {
		t.Fatal("current generation frame was not applied")
	}
}

func TestManagerRetryBudgetEndsDead(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)

	mgr := NewManager(Config{
		Hosts: map[string]config.Host{
			"remote": {SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"},
		},
		Dialer:      failingDialer{err: errors.New("dial failed")},
		RetryPolicy: remoteRetryPolicyForTest(2),
	})
	t.Cleanup(mgr.Close)

	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}

	waitForMirrorState(t, mgr, pane.ID, StateDead)
	snap, ok := mgr.Snapshot(pane.ID)
	if !ok {
		t.Fatal("mirror snapshot missing")
	}
	if snap.LastError != "remote connection retry budget exhausted" {
		t.Fatalf("LastError = %q, want retry budget exhausted", snap.LastError)
	}
}

func TestManagerAttachDialUsesAttemptTimeout(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	dialer := &blockingAttachDialer{}
	mgr := NewManager(Config{
		Hosts: map[string]config.Host{
			"remote": {SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"},
		},
		Dialer:        dialer,
		RetryPolicy:   remoteRetryPolicyForTest(1),
		AttachTimeout: time.Millisecond,
	})
	t.Cleanup(mgr.Close)

	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}

	start := time.Now()
	waitForMirrorState(t, mgr, pane.ID, StateDead)
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("mirror reached dead after %s, want attach timeout to bound blocked dial", elapsed)
	}
	if got := dialer.calls.Load(); got < 2 {
		t.Fatalf("dial calls = %d, want resolve and attach dial", got)
	}
}

func TestManagerAttachCommandErrorIsTerminal(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 2)
	dialer := &attachCommandErrorDialer{cmdErr: "attach failed"}
	mgr := NewManager(Config{
		Hosts: map[string]config.Host{
			"remote": {SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"},
		},
		Dialer:        dialer,
		AttachTimeout: time.Second,
	})
	t.Cleanup(mgr.Close)

	owner := &mirrorState{
		pane:  pane,
		ref:   checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state: StateConnecting,
	}
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = owner
	mgr.mu.Unlock()

	connected, terminal := mgr.attachAndRead(pane.ID, owner, StateConnecting)
	if !connected || !terminal {
		t.Fatalf("attachAndRead = (%v, %v), want connected terminal after command error", connected, terminal)
	}
	if got := dialer.calls.Load(); got != 2 {
		t.Fatalf("dial calls = %d, want resolve and attach only", got)
	}
	if !pane.ScreenContains("[attach failed]") {
		t.Fatal("pane screen missing command error marker")
	}
	snap, ok := mgr.Snapshot(pane.ID)
	if !ok || snap.State != StateDead {
		t.Fatalf("snapshot = (%+v, %v), want dead mirror", snap, ok)
	}
}

func TestManagerDetachReportsDetachedState(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}

	mgr.Detach(pane.ID)
	snap, ok := mgr.Snapshot(pane.ID)
	if !ok {
		t.Fatal("detached mirror snapshot missing")
	}
	if snap.State != StateDetached {
		t.Fatalf("state = %s, want %s", snap.State, StateDetached)
	}
}

func TestManagerNilReceiversAndTrackValidation(t *testing.T) {
	t.Parallel()

	var nilMgr *Manager
	nilMgr.Configure(Config{})
	nilMgr.Close()
	if err := nilMgr.Track(nil, checkpoint.RemoteRef{}); err == nil || err.Error() != "mirror manager is nil" {
		t.Fatalf("nil Track error = %v, want mirror manager is nil", err)
	}
	if n, err := nilMgr.Write(1, []byte("drop")); err != nil || n != len("drop") {
		t.Fatalf("nil Write = (%d, %v), want drop length and nil error", n, err)
	}
	if ref, ok := nilMgr.RemoteRef(1); ok || ref != nil {
		t.Fatalf("nil RemoteRef = (%v, %v), want nil false", ref, ok)
	}
	if snap, ok := nilMgr.Snapshot(1); ok || snap.State != "" {
		t.Fatalf("nil Snapshot = (%+v, %v), want zero false", snap, ok)
	}

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if err := mgr.Track(nil, checkpoint.RemoteRef{Host: "remote", PaneName: "agent"}); err == nil || err.Error() != "mirror pane is nil" {
		t.Fatalf("nil pane Track error = %v, want mirror pane is nil", err)
	}
	pane := newMirrorTestPane(t, 1)
	tests := []struct {
		name string
		ref  checkpoint.RemoteRef
		want string
	}{
		{name: "missing host", ref: checkpoint.RemoteRef{PaneName: "agent"}, want: "remote host is required"},
		{name: "missing pane name", ref: checkpoint.RemoteRef{Host: "remote"}, want: "remote pane name is required"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := mgr.Track(pane, tt.ref); err == nil || err.Error() != tt.want {
				t.Fatalf("Track error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestManagerConfigureStartsDeferredMirror(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	if err := mgr.Track(pane, ref); err != nil {
		t.Fatalf("Track: %v", err)
	}
	snap, ok := mgr.Snapshot(pane.ID)
	if !ok {
		t.Fatal("mirror snapshot missing")
	}
	if snap.LastError != `remote host "remote" is not configured` {
		t.Fatalf("LastError = %q, want missing host message", snap.LastError)
	}

	mgr.Configure(Config{
		Hosts: map[string]config.Host{
			"remote": {SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"},
		},
		Dialer:      failingDialer{err: errors.New("dial failed")},
		RetryPolicy: remoteRetryPolicyForTest(1),
	})
	waitForMirrorState(t, mgr, pane.ID, StateDead)
}

func TestManagerCloseClosesTrackedLinks(t *testing.T) {
	t.Parallel()

	link, _ := connectedTestLink(t)
	mgr := NewManager(Config{})
	mgr.mu.Lock()
	mgr.mirrors[1] = &mirrorState{link: link}
	mgr.mu.Unlock()

	mgr.Close()
	if err := link.WriteMsg(paneOutput("closed")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteMsg after Close error = %v, want net.ErrClosed", err)
	}
}

func TestManagerDetachClosesActiveLink(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	link, _ := connectedTestLink(t)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = &mirrorState{
		pane:         pane,
		ref:          checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state:        StateConnected,
		link:         link,
		remotePaneID: 9,
	}
	mgr.mu.Unlock()

	mgr.Detach(pane.ID)
	if err := link.WriteMsg(paneOutput("closed")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteMsg after Detach error = %v, want net.ErrClosed", err)
	}
}

func TestManagerWriteRoutesConnectedInputAndDropsWhenDisconnected(t *testing.T) {
	t.Parallel()

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if n, err := mgr.Write(1, nil); err != nil || n != 0 {
		t.Fatalf("empty Write = (%d, %v), want 0 nil", n, err)
	}
	if n, err := mgr.Write(1, []byte("drop")); err != nil || n != len("drop") {
		t.Fatalf("disconnected Write = (%d, %v), want drop length nil", n, err)
	}

	link, serverConn := connectedTestLink(t)
	mgr.mu.Lock()
	mgr.mirrors[1] = &mirrorState{
		state:        StateConnected,
		link:         link,
		remotePaneID: 42,
	}
	mgr.mu.Unlock()

	msgCh := make(chan struct {
		msg *proto.Message
		err error
	}, 1)
	go func() {
		msg, err := proto.NewReader(serverConn).ReadMsg()
		msgCh <- struct {
			msg *proto.Message
			err error
		}{msg: msg, err: err}
	}()
	if n, err := mgr.Write(1, []byte("hello")); err != nil || n != len("hello") {
		t.Fatalf("connected Write = (%d, %v), want input length nil", n, err)
	}
	got := <-msgCh
	if got.err != nil {
		t.Fatalf("ReadMsg: %v", got.err)
	}
	msg := got.msg
	if msg.Type != proto.MsgTypeInputPane || msg.PaneID != 42 || string(msg.PaneData) != "hello" {
		t.Fatalf("input message = %+v, want pane 42 hello", msg)
	}

	_ = serverConn.Close()
	if n, err := mgr.Write(1, []byte("closed")); err == nil || n != 0 {
		t.Fatalf("closed Write = (%d, %v), want 0 error", n, err)
	}
}

func TestManagerRemoteRefAndSnapshotMisses(t *testing.T) {
	t.Parallel()

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if ref, ok := mgr.RemoteRef(1); ok || ref != nil {
		t.Fatalf("missing RemoteRef = (%v, %v), want nil false", ref, ok)
	}
	if snap, ok := mgr.Snapshot(1); ok || snap.State != "" {
		t.Fatalf("missing Snapshot = (%+v, %v), want zero false", snap, ok)
	}

	pane := newMirrorTestPane(t, 1)
	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	if err := mgr.Track(pane, ref); err != nil {
		t.Fatalf("Track: %v", err)
	}
	gotRef, ok := mgr.RemoteRef(pane.ID)
	if !ok || gotRef == nil || *gotRef != ref {
		t.Fatalf("RemoteRef = (%v, %v), want %v true", gotRef, ok, ref)
	}
}

func TestManagerPrepareAttemptStopsWhenHostMissing(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	if err := mgr.Track(pane, checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}); err != nil {
		t.Fatalf("Track: %v", err)
	}

	_, _, ok := mgr.prepareAttempt(pane.ID, nil, StateConnecting)
	if ok {
		t.Fatal("prepareAttempt ok=true, want false for missing host")
	}
	snap, ok := mgr.Snapshot(pane.ID)
	if !ok {
		t.Fatal("mirror snapshot missing")
	}
	if snap.LastError != `remote host "remote" is not configured` {
		t.Fatalf("LastError = %q, want missing host message", snap.LastError)
	}
}

func TestManagerApplyMessageHistoryExitAndCommandError(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = &mirrorState{
		pane:       pane,
		ref:        checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state:      StateConnected,
		generation: 1,
	}
	mgr.mu.Unlock()

	if err := mgr.applyMessage(pane.ID, 1, nil); err != nil {
		t.Fatalf("apply nil message: %v", err)
	}
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypeLayout}); err != nil {
		t.Fatalf("apply ignored message: %v", err)
	}
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypePaneHistory, History: []string{"old-1", "old-2"}}); err != nil {
		t.Fatalf("apply history: %v", err)
	}
	if got := pane.ScrollbackLines(); len(got) != 2 || got[0] != "old-1" || got[1] != "old-2" {
		t.Fatalf("scrollback = %#v, want retained history", got)
	}
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypeCmdResult, CmdErr: "attach failed"}); err == nil || err.Error() != "attach failed" {
		t.Fatalf("apply command error = %v, want attach failed", err)
	}
	if !pane.ScreenContains("[attach failed]") {
		t.Fatal("pane screen missing command error marker")
	}

	mgr.mu.Lock()
	mgr.mirrors[pane.ID].state = StateConnected
	mgr.mu.Unlock()
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypeExit}); !errors.Is(err, errRemotePaneExited) {
		t.Fatalf("apply exit = %v, want errRemotePaneExited", err)
	}
	if !pane.ScreenContains("[remote pane exited]") {
		t.Fatal("pane screen missing remote pane exited marker")
	}
}

func TestManagerMarkDeadIsIdempotentAfterApplyMessageExit(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 2)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = &mirrorState{
		pane:       pane,
		ref:        checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state:      StateConnected,
		generation: 1,
	}
	mgr.mu.Unlock()

	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypeExit}); !errors.Is(err, errRemotePaneExited) {
		t.Fatalf("apply exit = %v, want errRemotePaneExited", err)
	}
	screenAfterApply, _ := pane.ScreenSnapshot()

	mgr.markDead(pane.ID, "remote pane exited")
	screenAfterMarkDead, _ := pane.ScreenSnapshot()
	if screenAfterMarkDead != screenAfterApply {
		t.Fatal("markDead wrote to pane when state was already StateDead")
	}
	if !pane.ScreenContains("[remote pane exited]") {
		t.Fatal("pane screen missing remote pane exited marker")
	}
}

func TestManagerOldOwnerCannotMutateRetrackedMirror(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 3)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	oldState := &mirrorState{
		pane:       pane,
		ref:        checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state:      StateReconnecting,
		generation: 1,
		running:    true,
	}
	newState := &mirrorState{
		pane:       pane,
		ref:        oldState.ref,
		state:      StateConnecting,
		generation: 1,
		running:    true,
	}
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = newState
	mgr.mu.Unlock()

	mgr.markStopped(pane.ID, oldState)
	if !newState.running {
		t.Fatal("old goroutine marked retracked mirror as stopped")
	}
	if gen, ok := mgr.markConnectedForOwner(pane.ID, oldState, 42, nil); ok || gen != 0 {
		t.Fatalf("old owner markConnected = (%d, %v), want 0 false", gen, ok)
	}
	if err := mgr.applyMessageForOwner(pane.ID, oldState, 1, paneOutput("old-frame")); err != nil {
		t.Fatalf("old owner applyMessage: %v", err)
	}
	if pane.ScreenContains("old-frame") {
		t.Fatal("old owner frame was applied to retracked mirror")
	}
	mgr.recordAttemptErrorForOwner(pane.ID, oldState, errors.New("old failure"))
	if newState.lastErr != "" {
		t.Fatalf("new mirror lastErr = %q, want unchanged", newState.lastErr)
	}
}

func TestManagerTerminalMessagesClearStoredLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *proto.Message
	}{
		{name: "exit", msg: &proto.Message{Type: proto.MsgTypeExit}},
		{name: "command error", msg: &proto.Message{Type: proto.MsgTypeCmdResult, CmdErr: "attach failed"}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pane := newMirrorTestPane(t, 4)
			link, _ := connectedTestLink(t)
			mgr := NewManager(Config{})
			t.Cleanup(mgr.Close)
			mgr.mu.Lock()
			mgr.mirrors[pane.ID] = &mirrorState{
				pane:       pane,
				ref:        checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
				state:      StateConnected,
				generation: 1,
				link:       link,
			}
			mgr.mu.Unlock()

			_ = mgr.applyMessage(pane.ID, 1, tt.msg)

			mgr.mu.Lock()
			got := mgr.mirrors[pane.ID].link
			mgr.mu.Unlock()
			if got != nil {
				t.Fatal("terminal message left stored link set")
			}
		})
	}
}

func TestManagerHistoryRefreshReplacesAfterBootstrap(t *testing.T) {
	t.Parallel()

	pane := newMirrorTestPane(t, 1)
	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	mgr.mu.Lock()
	mgr.mirrors[pane.ID] = &mirrorState{
		pane:          pane,
		ref:           checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"},
		state:         StateConnected,
		generation:    1,
		bootstrapping: true,
	}
	mgr.mu.Unlock()

	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypePaneHistory, History: []string{"boot-1"}}); err != nil {
		t.Fatalf("apply first bootstrap history: %v", err)
	}
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypePaneHistory, History: []string{"boot-2"}}); err != nil {
		t.Fatalf("apply second bootstrap history: %v", err)
	}
	if got := pane.ScrollbackLines(); len(got) != 2 || got[0] != "boot-1" || got[1] != "boot-2" {
		t.Fatalf("bootstrap scrollback = %#v, want appended chunks", got)
	}
	if err := mgr.applyMessage(pane.ID, 1, paneOutput("screen")); err != nil {
		t.Fatalf("apply bootstrap screen: %v", err)
	}
	if err := mgr.applyMessage(pane.ID, 1, &proto.Message{Type: proto.MsgTypePaneHistory, History: []string{"refreshed"}}); err != nil {
		t.Fatalf("apply refreshed history: %v", err)
	}
	if got := pane.ScrollbackLines(); len(got) != 1 || got[0] != "refreshed" {
		t.Fatalf("refreshed scrollback = %#v, want replaced history", got)
	}
}

func TestManagerMarkConnectedRejectsTerminalMirror(t *testing.T) {
	t.Parallel()

	mgr := NewManager(Config{})
	t.Cleanup(mgr.Close)
	mgr.mu.Lock()
	mgr.mirrors[1] = &mirrorState{state: StateDead}
	mgr.mu.Unlock()
	if gen, ok := mgr.markConnected(1, 42, nil); ok || gen != 0 {
		t.Fatalf("markConnected dead = (%d, %v), want 0 false", gen, ok)
	}
}

func paneOutput(text string) *proto.Message {
	return &proto.Message{Type: proto.MsgTypePaneOutput, PaneData: []byte(text)}
}

func newMirrorTestPane(t *testing.T, id uint32) *mux.Pane {
	t.Helper()
	pane := mux.NewProxyPaneWithScrollback(id, mux.PaneMeta{Name: "mirror", Host: "remote"}, 80, 23, mux.DefaultScrollbackLines, nil, nil, func(data []byte) (int, error) {
		return len(data), nil
	})
	t.Cleanup(func() {
		_ = pane.Close()
		_ = pane.WaitClosed()
	})
	return pane
}

func connectedTestLink(t *testing.T) (*remote.Link, net.Conn) {
	t.Helper()
	dialer := &pipeDialer{conns: make(chan net.Conn, 1)}
	link := remote.NewLink(config.Host{SSH: "ignored", Session: "main", SocketPath: "/tmp/amux-test"}, dialer)
	if err := link.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	serverConn := <-dialer.conns
	t.Cleanup(func() {
		_ = link.Close()
		_ = serverConn.Close()
	})
	return link, serverConn
}

func remoteRetryPolicyForTest(max int) remote.RetryPolicy {
	return remote.RetryPolicy{
		MaxAttempts:    max,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	}
}

type failingDialer struct {
	err error
}

func (d failingDialer) Dial(context.Context, config.Host) (net.Conn, error) {
	return nil, d.err
}

type pipeDialer struct {
	conns chan net.Conn
}

func (d *pipeDialer) Dial(context.Context, config.Host) (net.Conn, error) {
	serverConn, clientConn := net.Pipe()
	d.conns <- serverConn
	return clientConn, nil
}

type blockingAttachDialer struct {
	calls atomic.Int32
}

func (d *blockingAttachDialer) Dial(ctx context.Context, _ config.Host) (net.Conn, error) {
	call := d.calls.Add(1)
	if call%2 == 1 {
		serverConn, clientConn := net.Pipe()
		go serveResolvePane(serverConn, 42, "agent")
		return clientConn, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

type attachCommandErrorDialer struct {
	calls  atomic.Int32
	cmdErr string
}

func (d *attachCommandErrorDialer) Dial(_ context.Context, _ config.Host) (net.Conn, error) {
	call := d.calls.Add(1)
	serverConn, clientConn := net.Pipe()
	if call == 1 {
		go serveResolvePane(serverConn, 42, "agent")
	} else {
		go serveAttachCommandError(serverConn, d.cmdErr)
	}
	return clientConn, nil
}

func serveAttachCommandError(conn net.Conn, cmdErr string) {
	defer conn.Close()
	if _, err := proto.NewReader(conn).ReadMsg(); err != nil {
		return
	}
	_ = proto.NewWriter(conn).WriteMsg(&proto.Message{
		Type:   proto.MsgTypeCmdResult,
		CmdErr: cmdErr,
	})
}

func serveResolvePane(conn net.Conn, paneID uint32, paneName string) {
	defer conn.Close()
	if _, err := proto.NewReader(conn).ReadMsg(); err != nil {
		return
	}
	_ = proto.NewWriter(conn).WriteMsg(&proto.Message{
		Type: proto.MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{
			Root:  proto.CellSnapshot{IsLeaf: true, PaneID: paneID, Dir: -1},
			Panes: []proto.PaneSnapshot{{ID: paneID, Name: paneName}},
		},
	})
}

func waitForMirrorState(t *testing.T, mgr *Manager, paneID uint32, want State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if snap, ok := mgr.Snapshot(paneID); ok && snap.State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if snap, ok := mgr.Snapshot(paneID); ok {
		t.Fatalf("mirror state = %s, want %s (last error %q)", snap.State, want, snap.LastError)
	}
	t.Fatalf("mirror snapshot missing, want state %s", want)
}
