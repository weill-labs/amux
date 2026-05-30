package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mailbox"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
)

func TestClearCloexecReturnsErrnoOnInvalidFD(t *testing.T) {
	t.Parallel()

	err := clearCloexec(^uintptr(0))
	if err == nil {
		t.Fatal("clearCloexec() error = nil, want errno")
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno == 0 {
		t.Fatalf("clearCloexec() error = %v, want syscall.Errno", err)
	}
}

func TestServerReloadReturnsSessionShuttingDownBeforeCheckpoint(t *testing.T) {
	t.Parallel()

	sess := &Session{
		sessionEvents:    make(chan sessionEvent, 1),
		sessionEventStop: make(chan struct{}),
	}
	close(sess.sessionEventStop)

	srv := &Server{
		sessions: map[string]*Session{DefaultSessionName: sess},
	}

	err := srv.Reload("/definitely/missing")
	if !errors.Is(err, errSessionShuttingDown) {
		t.Fatalf("Reload() error = %v, want %v", err, errSessionShuttingDown)
	}
	if sess.shutdown.Load() {
		t.Fatal("Reload() should not mark session shutdown on early query failure")
	}
}

func TestServerReloadWritesCrashCheckpointBeforeExec(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reload-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer func() {
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("os.Remove(%q): %v", socketPath, err)
		}
	}()

	sess := newSession("reload-crash-checkpoint")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	startedAt := time.Date(2026, time.March, 27, 12, 0, 0, 0, time.UTC)
	sess.startedAt = startedAt

	mustSessionQuery(t, sess, func(sess *Session) struct{} {
		pane := newProxyPane(1, mux.PaneMeta{Name: "pane-1", Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())

		win := mux.NewWindow(pane, 80, 23)
		win.ID = 1
		win.Name = "window-1"

		seedSessionPanesForTest(sess, pane)
		sess.Windows = append(sess.Windows, win)
		sess.ActiveWindowID = win.ID
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
		return struct{}{}
	})

	srv := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sess.Name: sess},
		sockPath:     socketPath,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = srv

	err = srv.Reload(filepath.Join(t.TempDir(), "missing-amux"))
	if err == nil {
		t.Fatal("Reload() error = nil, want exec failure")
	}

	path := checkpoint.CrashCheckpointPathTimestamped(sess.Name, startedAt)
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("expected crash checkpoint at %s, err=%v", path, statErr)
	}

	cp, err := checkpoint.ReadCrash(path)
	if err != nil {
		t.Fatalf("ReadCrash(%q): %v", path, err)
	}
	if cp.SessionName != sess.Name {
		t.Fatalf("SessionName = %q, want %q", cp.SessionName, sess.Name)
	}
	if len(cp.PaneStates) != 1 || cp.PaneStates[0].Meta.Name != "pane-1" {
		t.Fatalf("PaneStates = %+v, want single pane-1", cp.PaneStates)
	}
}

func TestServerShutdownPreservesCrashCheckpointForCrashRestore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sess := newSession("graceful-crash-restore")
	defer stopSessionBackgroundLoops(t, sess)

	startedAt := time.Date(2026, time.March, 27, 12, 34, 56, 0, time.UTC)
	sess.startedAt = startedAt

	newPane := func(id uint32, name string) *mux.Pane {
		pane := newProxyPane(id, mux.PaneMeta{Name: name, Host: mux.DefaultHost, Color: "f5e0dc"}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		pane.SetOnClipboard(sess.clipboardCallback())
		pane.SetOnMetaUpdate(sess.metaCallback())
		return pane
	}

	pane1 := newPane(1, "pane-1")
	pane2 := newPane(2, "pane-2")
	win := mux.NewWindow(pane1, 80, 24)
	win.ID = 1
	win.Name = "graceful"
	if _, err := win.Split(mux.SplitVertical, pane2); err != nil {
		t.Fatalf("win.Split: %v", err)
	}

	mustSessionMutation(t, sess, func(sess *Session) {
		seedSessionPanesForTest(sess, pane1, pane2)
		sess.Windows = []*mux.Window{win}
		sess.ActiveWindowID = win.ID
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
	})

	srv := &Server{
		sessions:     map[string]*Session{sess.Name: sess},
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = srv

	crashPath := checkpoint.CrashCheckpointPathTimestamped(sess.Name, startedAt)
	srv.Shutdown()

	if _, statErr := os.Stat(crashPath); statErr != nil {
		t.Fatalf("crash checkpoint should survive clean shutdown, err=%v", statErr)
	}

	cp, err := checkpoint.ReadCrash(crashPath)
	if err != nil {
		t.Fatalf("ReadCrash(%q): %v", crashPath, err)
	}
	if len(cp.Layout.Windows) != 1 || cp.Layout.Windows[0].Name != "graceful" {
		t.Fatalf("restorable checkpoint windows = %+v, want single graceful window", cp.Layout.Windows)
	}
	if len(cp.PaneStates) != 2 {
		t.Fatalf("PaneStates = %d, want 2", len(cp.PaneStates))
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sess.Name, cp, crashPath, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	restoredSess := restored.firstSession()
	if len(restoredSess.Windows) != 1 || restoredSess.Windows[0].Name != "graceful" {
		t.Fatalf("restored windows = %+v, want single graceful window", restoredSess.Windows)
	}
	if len(restoredSess.Panes) != 2 {
		t.Fatalf("restored panes = %d, want 2", len(restoredSess.Panes))
	}
}

func TestBuildCrashCheckpointPreservesMirrorRemoteRef(t *testing.T) {
	t.Parallel()

	sess := newSession("mirror-crash-checkpoint")
	defer stopSessionBackgroundLoops(t, sess)

	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	mustSessionMutation(t, sess, func(sess *Session) {
		pane := newProxyPane(1, mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)}, 80, 23,
			sess.paneOutputCallback(), sess.paneExitCallback(), sess.mirrorWriteOverride(1),
		)
		win := mux.NewWindow(pane, 80, 24)
		win.ID = 1
		win.Name = "window-1"
		seedSessionPanesForTest(sess, pane)
		sess.Windows = []*mux.Window{win}
		sess.ActiveWindowID = win.ID
		if err := sess.trackMirrorPane(pane, ref); err != nil {
			t.Fatalf("trackMirrorPane: %v", err)
		}
	})

	cp := sess.buildCrashCheckpoint()
	if cp == nil || len(cp.PaneStates) != 1 {
		t.Fatalf("crash checkpoint = %+v, want one pane state", cp)
	}
	if got := cp.PaneStates[0].RemoteRef; got == nil || *got != ref {
		t.Fatalf("RemoteRef = %+v, want %+v", got, ref)
	}
}

func TestCrashRestoreTracksMirrorRemoteRef(t *testing.T) {
	t.Parallel()

	sessionName := fmt.Sprintf("mirror-crash-restore-%d", time.Now().UnixNano())
	ref := checkpoint.RemoteRef{Host: "remote", Session: "main", PaneName: "agent"}
	cp := &checkpoint.CrashCheckpoint{
		Version:       checkpoint.CrashVersion,
		SessionName:   sessionName,
		Counter:       1,
		WindowCounter: 1,
		Layout: proto.LayoutSnapshot{
			SessionName:  sessionName,
			Width:        80,
			Height:       24,
			ActivePaneID: 1,
			Root: proto.CellSnapshot{
				X: 0, Y: 0, W: 80, H: 24, IsLeaf: true, Dir: -1, PaneID: 1,
			},
			Panes: []proto.PaneSnapshot{{
				ID:    1,
				Name:  "mirror",
				Host:  "remote",
				Color: config.AccentColor(0),
			}},
		},
		PaneStates: []checkpoint.CrashPaneState{{
			ID:        1,
			Meta:      mux.PaneMeta{Name: "mirror", Host: "remote", Color: config.AccentColor(0)},
			Cols:      80,
			Rows:      23,
			Screen:    "cached remote screen",
			IsProxy:   true,
			RemoteRef: &ref,
		}},
		Timestamp: time.Now(),
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, "", mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	restoredSess := restored.firstSession()
	got, ok := restoredSess.mirror.RemoteRef(1)
	if !ok || got == nil || *got != ref {
		t.Fatalf("RemoteRef = (%+v, %v), want %+v true", got, ok, ref)
	}
}

func TestReloadCheckpointRestoresMailboxMessagesAndState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sessionName := fmt.Sprintf("mailbox-reload-%d", time.Now().UnixNano())
	sess, _, _, _, cleanup := newMailboxCheckpointSession(t, sessionName)
	defer cleanup()

	root, reply, _ := seedMailboxCheckpointState(t, sess)

	cp, err := sess.buildReloadCheckpoint()
	if err != nil {
		t.Fatalf("buildReloadCheckpoint: %v", err)
	}
	cp.ListenerFd = newRestoreListenerFD(t)

	restored, err := NewServerFromCheckpointWithScrollback(cp, mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	assertRestoredMailboxCheckpointState(t, restored, root.ID, reply.ID)
}

func TestCrashCheckpointRestoresMailboxMessagesAndState(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sessionName := fmt.Sprintf("mailbox-crash-%d", time.Now().UnixNano())
	sess, _, _, _, cleanup := newMailboxCheckpointSession(t, sessionName)
	defer cleanup()

	root, reply, _ := seedMailboxCheckpointState(t, sess)

	cp := sess.buildCrashCheckpoint()
	if cp == nil {
		t.Fatal("buildCrashCheckpoint returned nil")
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, "", mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()

	assertRestoredMailboxCheckpointState(t, restored, root.ID, reply.ID)
}

func TestCrashRestoreAdvancesMailboxEventSequencePastRestoredDeliveries(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	sessionName := fmt.Sprintf("mailbox-seq-%d", time.Now().UnixNano())
	sess, _, _, _, cleanup := newMailboxCheckpointSession(t, sessionName)
	defer cleanup()

	_, _, lastSeq := seedMailboxCheckpointState(t, sess)
	cp := sess.buildCrashCheckpoint()
	if cp == nil {
		t.Fatal("buildCrashCheckpoint returned nil")
	}

	restored, err := NewServerFromCrashCheckpointWithScrollback(sessionName, cp, "", mux.DefaultScrollbackLines)
	if err != nil {
		t.Fatalf("NewServerFromCrashCheckpointWithScrollback: %v", err)
	}
	defer restored.Shutdown()
	restoredSess := restored.firstSession()

	resultCh := make(chan struct {
		output string
		cmdErr string
	}, 1)
	go func() {
		resultCh <- runTestCommand(t, restored, restoredSess, "wait", "msg", "pane-2", "--after", fmt.Sprintf("%d", lastSeq), "--timeout", "5s", "--format", "json")
	}()
	waitForMailboxWaitSubscription(t, restoredSess)

	next, err := restoredSess.enqueueMailboxSend(context.Background(), mailbox.SendRequest{
		Sender:     mailbox.PaneAddress{ID: 1, Name: "pane-1", Host: mux.DefaultHost},
		Recipients: []mailbox.PaneAddress{{ID: 2, Name: "pane-2", Host: mux.DefaultHost}},
		Subject:    "After restore",
		Body:       []byte("new body"),
	})
	if err != nil {
		t.Fatalf("enqueueMailboxSend after restore: %v", err)
	}

	res := readWaitMessageResult(t, resultCh)
	if res.cmdErr != "" {
		t.Fatalf("wait msg after restored seq error = %q", res.cmdErr)
	}
	var summary proto.MailboxMessageSummary
	if err := json.Unmarshal([]byte(res.output), &summary); err != nil {
		t.Fatalf("unmarshal wait msg output %q: %v", res.output, err)
	}
	if summary.ID != string(next.ID) {
		t.Fatalf("wait msg returned %q, want new message %q", summary.ID, next.ID)
	}
	if summary.LastEventSeq <= lastSeq {
		t.Fatalf("new message event seq = %d, want > restored seq %d", summary.LastEventSeq, lastSeq)
	}
}

func TestMailboxCheckpointSnapshotSkipsNilAndEmptyStores(t *testing.T) {
	t.Parallel()

	if got := mailboxCheckpointSnapshot(nil); got != nil {
		t.Fatalf("mailboxCheckpointSnapshot(nil) = %#v, want nil", got)
	}
	if got := mailboxCheckpointSnapshot(mailbox.NewStore(mailbox.Options{})); got != nil {
		t.Fatalf("mailboxCheckpointSnapshot(empty) = %#v, want nil", got)
	}
}

func TestRestoreMailboxHandlesNilAndMalformedSnapshots(t *testing.T) {
	t.Parallel()

	sess := newSession("mailbox-restore-errors")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	if err := sess.restoreMailbox(nil, 99); err != nil {
		t.Fatalf("restoreMailbox(nil): %v", err)
	}
	if sess.mailboxEventSeq != 0 {
		t.Fatalf("mailboxEventSeq after nil restore = %d, want unchanged zero", sess.mailboxEventSeq)
	}

	err := sess.restoreMailbox(&mailbox.Snapshot{
		Deliveries: []mailbox.DeliveryState{{MessageID: "msg-000001", Recipient: mailbox.PaneAddress{ID: 2, Name: "pane-2"}}},
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "missing message") {
		t.Fatalf("restoreMailbox malformed error = %v, want missing message", err)
	}
}

func TestBuildReloadCheckpointRequiresWindow(t *testing.T) {
	t.Parallel()

	sess := newSession("mailbox-reload-no-window")
	stopCrashCheckpointLoop(t, sess)
	t.Cleanup(func() {
		stopSessionBackgroundLoops(t, sess)
	})

	if _, err := sess.buildReloadCheckpoint(); err == nil || !strings.Contains(err.Error(), "no window") {
		t.Fatalf("buildReloadCheckpoint error = %v, want no window", err)
	}
}

func newMailboxCheckpointSession(t *testing.T, sessionName string) (*Session, *mux.Pane, *mux.Pane, *mux.Pane, func()) {
	t.Helper()

	sess := newSession(sessionName)
	stopCrashCheckpointLoop(t, sess)

	p1 := newTestPane(sess, 1, "pane-1")
	p2 := newTestPane(sess, 2, "pane-2")
	p3 := newTestPane(sess, 3, "pane-3")
	w := newTestWindowWithPanes(t, sess, 1, "main", p1, p2, p3)
	setSessionLayoutForTest(t, sess, w.ID, []*mux.Window{w}, p1, p2, p3)

	cleanup := func() {
		sess.shutdown.Store(true)
		stopSessionBackgroundLoops(t, sess)
		for _, pane := range []*mux.Pane{p1, p2, p3} {
			_ = pane.Close()
			_ = pane.WaitClosed()
		}
	}
	return sess, p1, p2, p3, cleanup
}

func seedMailboxCheckpointState(t *testing.T, sess *Session) (mailbox.Message, mailbox.Message, uint64) {
	t.Helper()

	root, err := sess.enqueueMailboxSend(context.Background(), mailbox.SendRequest{
		Sender:     mailbox.PaneAddress{ID: 1, Name: "pane-1", Host: mux.DefaultHost},
		Recipients: []mailbox.PaneAddress{{ID: 2, Name: "pane-2", Host: mux.DefaultHost}},
		Topics:     []string{"review"},
		Groups:     []string{"agents"},
		Subject:    "Root",
		Body:       []byte("root body"),
		Metadata: map[string]json.RawMessage{
			"priority": json.RawMessage(`"high"`),
		},
	})
	if err != nil {
		t.Fatalf("enqueueMailboxSend root: %v", err)
	}
	if _, _, err := sess.enqueueMailboxRead(context.Background(), root.ID, 2, mailbox.ReadOptions{}); err != nil {
		t.Fatalf("enqueueMailboxRead root: %v", err)
	}

	reply, err := sess.enqueueMailboxSend(context.Background(), mailbox.SendRequest{
		Sender:     mailbox.PaneAddress{ID: 2, Name: "pane-2", Host: mux.DefaultHost},
		Recipients: []mailbox.PaneAddress{{ID: 1, Name: "pane-1", Host: mux.DefaultHost}},
		Subject:    "Reply",
		Body:       []byte("reply body"),
		ReplyTo:    root.ID,
	})
	if err != nil {
		t.Fatalf("enqueueMailboxSend reply: %v", err)
	}
	if _, err := sess.enqueueMailboxAck(context.Background(), reply.ID, 1, mailbox.AckRequest{Status: "seen", Note: "queued"}); err != nil {
		t.Fatalf("enqueueMailboxAck reply: %v", err)
	}

	summary := mustSessionQuery(t, sess, func(sess *Session) mailbox.DeliverySummary {
		summary, err := sess.mailbox.DeliverySummary(reply.ID, 1)
		if err != nil {
			t.Fatalf("DeliverySummary reply: %v", err)
		}
		return summary
	})
	return root, reply, summary.LastEventSeq
}

func assertRestoredMailboxCheckpointState(t *testing.T, srv *Server, rootID, replyID mailbox.MessageID) {
	t.Helper()

	sess := srv.firstSession()
	if sess == nil {
		t.Fatal("restored server has no session")
	}

	root, ok := sess.mailbox.Message(rootID)
	if !ok {
		t.Fatalf("restored root %q not found", rootID)
	}
	if root.ThreadID != mailbox.ThreadID(rootID) || len(root.Replies) != 1 || root.Replies[0] != replyID {
		t.Fatalf("restored root thread fields = (%q, %#v), want reply %q", root.ThreadID, root.Replies, replyID)
	}
	if got := string(root.Metadata["priority"]); got != `"high"` {
		t.Fatalf("restored metadata priority = %s, want high", got)
	}

	rootDelivery, err := sess.mailbox.DeliverySummary(rootID, 2)
	if err != nil {
		t.Fatalf("root DeliverySummary: %v", err)
	}
	if rootDelivery.ReadAt.IsZero() || rootDelivery.LastEventSeq == 0 {
		t.Fatalf("root delivery = %#v, want read timestamp and event sequence", rootDelivery)
	}

	reply, ok := sess.mailbox.Message(replyID)
	if !ok {
		t.Fatalf("restored reply %q not found", replyID)
	}
	if reply.ThreadID != mailbox.ThreadID(rootID) || reply.InReplyTo != rootID {
		t.Fatalf("restored reply thread fields = (%q, %q), want (%q, %q)", reply.ThreadID, reply.InReplyTo, rootID, rootID)
	}
	replyDelivery, err := sess.mailbox.DeliverySummary(replyID, 1)
	if err != nil {
		t.Fatalf("reply DeliverySummary: %v", err)
	}
	if replyDelivery.AckedAt.IsZero() || replyDelivery.AckStatus != "seen" || replyDelivery.AckNote != "queued" || replyDelivery.LastEventSeq == 0 {
		t.Fatalf("reply delivery = %#v, want ack state and event sequence", replyDelivery)
	}

	read := runTestCommand(t, srv, sess, "msg", "read", string(rootID), "--for", "pane-2", "--peek", "--format", "json")
	if read.cmdErr != "" {
		t.Fatalf("msg read restored root error: %s", read.cmdErr)
	}
	readJSON := parseMsgCommandReadJSON(t, read.output)
	if readJSON.Body != "root body" || readJSON.ReadAt == "" {
		t.Fatalf("restored msg read JSON = %#v, want body and persisted read_at", readJSON)
	}
	if got := string(readJSON.Metadata["priority"]); got != `"high"` {
		t.Fatalf("restored msg read metadata priority = %s, want high", got)
	}

	next, err := sess.enqueueMailboxSend(context.Background(), mailbox.SendRequest{
		Sender:     mailbox.PaneAddress{ID: 3, Name: "pane-3", Host: mux.DefaultHost},
		Recipients: []mailbox.PaneAddress{{ID: 2, Name: "pane-2", Host: mux.DefaultHost}},
		Subject:    "Next",
		Body:       []byte("next body"),
	})
	if err != nil {
		t.Fatalf("enqueueMailboxSend next: %v", err)
	}
	if next.ID != "msg-000003" {
		t.Fatalf("next message ID = %q, want msg-000003", next.ID)
	}
}

func newRestoreListenerFD(t *testing.T) int {
	t.Helper()

	path := filepath.Join(t.TempDir(), "restore.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen(%q): %v", path, err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	fd, err := listenerFd(listener)
	if err != nil {
		t.Fatalf("listenerFd: %v", err)
	}
	return fd
}
