package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
	mirrorpkg "github.com/weill-labs/amux/internal/server/mirror"
)

func TestMirrorManagerHappyPath(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	mirrorPane := h.attachMirror(t)
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateConnected)

	h.remotePane.FeedOutput([]byte("REMOTE-HAPPY-PATH"))
	waitUntil(t, func() bool {
		return mirrorPane.ScreenContains("REMOTE-HAPPY-PATH")
	})

	if _, err := mirrorPane.Write([]byte("input-to-remote")); err != nil {
		t.Fatalf("mirror Write: %v", err)
	}
	select {
	case got := <-h.remoteWrites:
		if string(got) != "input-to-remote" {
			t.Fatalf("remote input = %q, want input-to-remote", got)
		}
	case <-time.After(time.Second):
		t.Fatal("input did not reach remote pane")
	}
}

func TestMirrorManagerForwardsPaneMetaWithoutOverwritingHost(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	mirrorPane := h.attachMirror(t)
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateConnected)
	h.waitRemoteScopedClientReady(t)

	mustSessionMutation(t, h.remoteSess, func(sess *Session) {
		h.remotePane.Meta.Host = mux.DefaultHost
		h.remotePane.Meta.GitBranch = "feature/federation"
		h.remotePane.Meta.PR = "826"
		h.remotePane.Meta.TrackedPRs = []proto.TrackedPR{{
			Number: 826,
			Status: proto.TrackedStatusActive,
		}}
		h.remotePane.Meta.TrackedIssues = []proto.TrackedIssue{{
			ID:     "LAB-1963",
			Status: proto.TrackedStatusActive,
		}}
		sess.broadcastLayoutNow()
	})

	waitUntil(t, func() bool {
		return mirrorPane.Meta.GitBranch == "feature/federation" &&
			mirrorPane.Meta.PR == "826" &&
			len(mirrorPane.Meta.TrackedPRs) == 1 &&
			len(mirrorPane.Meta.TrackedIssues) == 1
	})
	if mirrorPane.Meta.Host != mirrorRemoteHostName {
		t.Fatalf("mirror Host = %q, want %q", mirrorPane.Meta.Host, mirrorRemoteHostName)
	}
	if mirrorPane.Meta.TrackedPRs[0].Number != 826 {
		t.Fatalf("TrackedPRs = %+v, want PR 826", mirrorPane.Meta.TrackedPRs)
	}
	if mirrorPane.Meta.TrackedIssues[0].ID != "LAB-1963" {
		t.Fatalf("TrackedIssues = %+v, want LAB-1963", mirrorPane.Meta.TrackedIssues)
	}
}

func TestMirrorManagerCaptureUsesForwardedAgentStatusForProxyPane(t *testing.T) {
	t.Parallel()

	localSrv, localSess, cleanup := newCommandTestSession(t)
	defer cleanup()
	dialer := newScriptedMetaDialer(proto.PaneMetaUpdate{
		GitBranch: "feature/federation",
		PR:        "826",
		AgentStatus: proto.PaneAgentStatus{
			Exited:         false,
			CurrentCommand: "codex",
			Idle:           false,
			LastOutput:     "2026-05-29T12:00:01Z",
		},
	})
	defer dialer.Close()
	localSess.mirror.Configure(mirrorpkg.Config{
		Hosts:       mirrorHosts(localSess.Name),
		Dialer:      dialer,
		RetryPolicy: mirrorRetryPolicyForTest(),
	})

	localPane := newTestPane(localSess, 1, "local")
	localWindow := newTestWindowWithPanes(t, localSess, 1, "local-window", localPane)
	setSessionLayoutForTest(t, localSess, localWindow.ID, []*mux.Window{localWindow}, localPane)
	ref := checkpoint.RemoteRef{Host: mirrorRemoteHostName, Session: localSess.Name, PaneName: "remote-agent"}
	mirrorPane, err := enqueueSessionQueryOnState(localSess.context(), localSess, func(sess *Session) (*mux.Pane, error) {
		pane, err := sess.prepareMirrorPane(mux.PaneMeta{Name: "mirror-agent"}, ref, 80, 22)
		if err != nil {
			return nil, err
		}
		if _, err := localWindow.SplitPaneWithOptions(localPane.ID, mux.SplitHorizontal, pane, mux.SplitOptions{}); err != nil {
			return nil, err
		}
		if err := sess.trackMirrorPane(pane, ref); err != nil {
			return nil, err
		}
		sess.broadcastLayoutNow()
		return pane, nil
	})
	if err != nil {
		t.Fatalf("attach scripted mirror: %v", err)
	}
	waitUntil(t, func() bool {
		status, ok := localSess.mirror.AgentStatus(mirrorPane.ID)
		return ok && !status.Exited && status.CurrentCommand == "codex"
	})

	var got proto.CapturePane
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got = captureJSONPaneFromCommand(t, localSrv, localSess, mirrorPane.ID)
		if !got.Exited && !got.Idle && got.CurrentCommand == "codex" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("mirror capture agent status = exited=%t current_command=%q idle=%t, want busy codex forwarded status", got.Exited, got.CurrentCommand, got.Idle)
}

func TestMirrorManagerRemotePaneKilledGoesDead(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	mirrorPane := h.attachMirror(t)
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateConnected)

	h.remoteSess.enqueuePaneExit(h.remotePane.ID, "exit 0")
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateDead)
}

func TestMirrorManagerRemoteServerDropExhaustsRetryBudget(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	mirrorPane := h.attachMirror(t)
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateConnected)

	h.dialer.fail.Store(true)
	h.dialer.closeAll()
	h.waitMirrorState(t, mirrorPane.ID, mirrorpkg.StateDead)
}

func TestMirrorManagerCheckpointRestoreRehydrates(t *testing.T) {
	t.Parallel()

	h := newMirrorIntegrationHarness(t)
	defer h.cleanup()

	restoreDir := t.TempDir()
	sessionName := "mirror-restore-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	listenerPath := filepath.Join(restoreDir, "restore.sock")
	ln, err := net.Listen("unix", listenerPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	listenerFD, err := listenerFd(ln)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}
	restoreFD, err := syscall.Dup(listenerFD)
	if err != nil {
		t.Fatalf("dup listener fd: %v", err)
	}
	if err := ln.Close(); err != nil {
		_ = syscall.Close(restoreFD)
		t.Fatalf("closing original listener: %v", err)
	}

	ref := checkpoint.RemoteRef{Host: mirrorRemoteHostName, Session: h.remoteSess.Name, PaneName: h.remotePane.Meta.Name}
	cp := &checkpoint.ServerCheckpoint{
		Version:       checkpoint.ServerCheckpointVersion,
		SessionName:   sessionName,
		Counter:       1,
		WindowCounter: 1,
		ListenerFd:    restoreFD,
		Layout:        protoLayoutForMirrorCheckpoint(sessionName),
		Panes: []checkpoint.PaneCheckpoint{{
			ID:        1,
			Meta:      mux.PaneMeta{Name: "mirror-restored", Host: mirrorRemoteHostName, Color: config.AccentColor(0)},
			PtmxFd:    -1,
			Cols:      80,
			Rows:      23,
			IsProxy:   true,
			RemoteRef: &ref,
		}},
	}

	restored, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()
	restored.ConfigureMirrors(h.hosts(), h.dialer)

	restoredSess := restored.firstSession()
	if restoredSess == nil {
		t.Fatal("restored server missing session")
	}
	var restoredPane *mux.Pane
	waitUntil(t, func() bool {
		restoredPane = restoredSess.findPaneByID(1)
		if restoredPane == nil {
			return false
		}
		snap, ok := restoredSess.mirror.Snapshot(restoredPane.ID)
		return ok && snap.State == mirrorpkg.StateConnected
	})

	h.remotePane.FeedOutput([]byte("CHECKPOINT-REHYDRATED"))
	waitUntil(t, func() bool {
		return restoredPane.ScreenContains("CHECKPOINT-REHYDRATED")
	})
}

const mirrorRemoteHostName = "remote"

type mirrorIntegrationHarness struct {
	localSrv     *Server
	localSess    *Session
	remoteSrv    *Server
	remoteSess   *Session
	remotePane   *mux.Pane
	remoteWrites chan []byte
	dialer       *serverDialer
	cleanup      func()
}

func newMirrorIntegrationHarness(t *testing.T) *mirrorIntegrationHarness {
	t.Helper()

	remoteSrv, remoteSess, remoteCleanup := newCommandTestSession(t)
	remoteWrites := make(chan []byte, 8)
	remotePane := newProxyPane(1, mux.PaneMeta{
		Name:  "remote-agent",
		Host:  mux.DefaultHost,
		Color: config.AccentColor(0),
	}, 80, 23, remoteSess.paneOutputCallback(), remoteSess.paneExitCallback(), func(data []byte) (int, error) {
		remoteWrites <- append([]byte(nil), data...)
		return len(data), nil
	})
	remotePane = remoteSess.ownPane(remotePane)
	remoteWindow := newTestWindowWithPanes(t, remoteSess, 1, "remote-window", remotePane)
	setSessionLayoutForTest(t, remoteSess, remoteWindow.ID, []*mux.Window{remoteWindow}, remotePane)

	localSrv, localSess, localCleanup := newCommandTestSession(t)
	localPane := newTestPane(localSess, 1, "local")
	localWindow := newTestWindowWithPanes(t, localSess, 1, "local-window", localPane)
	setSessionLayoutForTest(t, localSess, localWindow.ID, []*mux.Window{localWindow}, localPane)

	dialer := &serverDialer{srv: remoteSrv}
	localSess.mirror.Configure(mirrorpkg.Config{
		Hosts:       mirrorHosts(remoteSess.Name),
		Dialer:      dialer,
		RetryPolicy: mirrorRetryPolicyForTest(),
	})

	return &mirrorIntegrationHarness{
		localSrv:     localSrv,
		localSess:    localSess,
		remoteSrv:    remoteSrv,
		remoteSess:   remoteSess,
		remotePane:   remotePane,
		remoteWrites: remoteWrites,
		dialer:       dialer,
		cleanup: func() {
			localSess.mirror.Close()
			remoteCleanup()
			localCleanup()
		},
	}
}

func (h *mirrorIntegrationHarness) hosts() map[string]config.Host {
	return mirrorHosts(h.remoteSess.Name)
}

func (h *mirrorIntegrationHarness) attachMirror(t *testing.T) *mux.Pane {
	t.Helper()
	ref := checkpoint.RemoteRef{
		Host:     mirrorRemoteHostName,
		Session:  h.remoteSess.Name,
		PaneName: h.remotePane.Meta.Name,
	}
	pane, err := enqueueSessionQueryOnState(h.localSess.context(), h.localSess, func(sess *Session) (*mux.Pane, error) {
		w := sess.activeWindow()
		if w == nil {
			return nil, errors.New("missing local window")
		}
		pane, err := sess.prepareMirrorPane(mux.PaneMeta{Name: "mirror-agent"}, ref, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return nil, err
		}
		if _, err := w.SplitPaneWithOptions(w.ActivePane.ID, mux.SplitHorizontal, pane, mux.SplitOptions{}); err != nil {
			sess.removePane(pane.ID)
			sess.closePaneAsync(pane)
			return nil, err
		}
		if err := sess.trackMirrorPane(pane, ref); err != nil {
			return nil, err
		}
		sess.broadcastLayoutNow()
		return pane, nil
	})
	if err != nil {
		t.Fatalf("attach mirror: %v", err)
	}
	return pane
}

func (h *mirrorIntegrationHarness) waitMirrorState(t *testing.T, paneID uint32, want mirrorpkg.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if snap, ok := h.localSess.mirror.Snapshot(paneID); ok && snap.State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if snap, ok := h.localSess.mirror.Snapshot(paneID); ok {
		t.Fatalf("mirror state = %s, want %s (last error %q)", snap.State, want, snap.LastError)
	}
	t.Fatalf("mirror %d missing, want state %s", paneID, want)
}

func (h *mirrorIntegrationHarness) waitRemoteScopedClientReady(t *testing.T) {
	t.Helper()
	waitUntil(t, func() bool {
		return mustSessionQuery(t, h.remoteSess, func(sess *Session) bool {
			for _, cc := range sess.ensureClientManager().snapshotClients() {
				if cc.isScopedToPane(h.remotePane.ID) && !cc.isBootstrapping() {
					return true
				}
			}
			return false
		})
	})
}

func captureJSONPaneFromCommand(t *testing.T, srv *Server, sess *Session, paneID uint32) proto.CapturePane {
	t.Helper()

	res := runTestCommand(t, srv, sess, "capture", "--format", "json")
	if res.cmdErr != "" {
		t.Fatalf("capture --format json cmdErr = %q", res.cmdErr)
	}
	var capture proto.CaptureJSON
	if err := json.Unmarshal([]byte(res.output), &capture); err != nil {
		t.Fatalf("unmarshal capture json: %v\n%s", err, res.output)
	}
	for _, pane := range capture.Panes {
		if pane.ID == paneID {
			return pane
		}
	}
	t.Fatalf("capture missing pane %d: %s", paneID, res.output)
	return proto.CapturePane{}
}

func mirrorHosts(session string) map[string]config.Host {
	return map[string]config.Host{
		mirrorRemoteHostName: {
			SSH:        "ignored",
			Session:    session,
			SocketPath: "/tmp/amux-mirror-test",
		},
	}
}

func mirrorRetryPolicyForTest() remote.RetryPolicy {
	return remote.RetryPolicy{
		MaxAttempts:    2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
	}
}

func protoLayoutForMirrorCheckpoint(sessionName string) proto.LayoutSnapshot {
	return proto.LayoutSnapshot{
		SessionName:  sessionName,
		Width:        80,
		Height:       23,
		ActivePaneID: 1,
		Root: proto.CellSnapshot{
			X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 1,
		},
		Panes: []proto.PaneSnapshot{{
			ID:    1,
			Name:  "mirror-restored",
			Host:  mirrorRemoteHostName,
			Color: config.AccentColor(0),
		}},
	}
}

type serverDialer struct {
	srv *Server

	fail  atomic.Bool
	mu    sync.Mutex
	conns []net.Conn
}

func (d *serverDialer) Dial(ctx context.Context, _ config.Host) (net.Conn, error) {
	if d.fail.Load() {
		return nil, errors.New("remote server unavailable")
	}
	serverConn, clientConn := net.Pipe()
	d.mu.Lock()
	d.conns = append(d.conns, clientConn)
	d.mu.Unlock()
	go d.srv.handleConn(serverConn)
	go func() {
		<-ctx.Done()
		_ = clientConn.Close()
	}()
	return clientConn, nil
}

func (d *serverDialer) closeAll() {
	d.mu.Lock()
	conns := append([]net.Conn(nil), d.conns...)
	d.conns = nil
	d.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

type scriptedMetaDialer struct {
	update proto.PaneMetaUpdate
	dials  atomic.Int32
	done   chan struct{}
}

func newScriptedMetaDialer(update proto.PaneMetaUpdate) *scriptedMetaDialer {
	return &scriptedMetaDialer{
		update: update,
		done:   make(chan struct{}),
	}
}

func (d *scriptedMetaDialer) Dial(_ context.Context, _ config.Host) (net.Conn, error) {
	serverConn, clientConn := net.Pipe()
	switch d.dials.Add(1) {
	case 1:
		go d.serveListPanes(serverConn)
	default:
		go d.serveAttachPane(serverConn)
	}
	return clientConn, nil
}

func (d *scriptedMetaDialer) Close() {
	close(d.done)
}

func (d *scriptedMetaDialer) serveListPanes(conn net.Conn) {
	defer conn.Close()
	msg, err := proto.ReadMsg(conn)
	if err != nil || msg.Type != proto.MsgTypeListPanes {
		return
	}
	_ = proto.WriteMsg(conn, &proto.Message{
		Type: proto.MsgTypeLayout,
		Layout: &proto.LayoutSnapshot{
			SessionName: msg.Session,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 23, IsLeaf: true, Dir: -1, PaneID: 42,
			},
			Panes: []proto.PaneSnapshot{{
				ID:   42,
				Name: "remote-agent",
				Host: mux.DefaultHost,
			}},
		},
	})
}

func (d *scriptedMetaDialer) serveAttachPane(conn net.Conn) {
	defer conn.Close()
	msg, err := proto.ReadMsg(conn)
	if err != nil || msg.Type != proto.MsgTypeAttachPane || msg.PaneID != 42 {
		return
	}
	_ = proto.WriteMsg(conn, &proto.Message{
		Type:           proto.MsgTypePaneMetaUpdate,
		PaneID:         42,
		PaneMetaUpdate: &d.update,
	})
	select {
	case <-d.done:
	case <-time.After(5 * time.Second):
	}
}
