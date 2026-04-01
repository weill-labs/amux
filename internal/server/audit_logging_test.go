package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	ckpt "github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/mux"
)

var auditEnvGate = func() chan struct{} {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	return ch
}()

func newAuditTestLogger() (*charmlog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := auditlog.New(&buf, auditlog.Options{
		Format: auditlog.FormatJSON,
		Level:  charmlog.DebugLevel,
	})
	return logger, &buf
}

func parseAuditRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("json.Unmarshal(%q): %v", string(line), err)
		}
		records = append(records, record)
	}
	return records
}

func findAuditRecord(records []map[string]any, event string) map[string]any {
	for _, record := range records {
		if record["event"] == event {
			return record
		}
	}
	return nil
}

func lockAuditStateHome(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	<-auditEnvGate
	prev, hadPrev := os.LookupEnv("XDG_STATE_HOME")
	if err := os.Setenv("XDG_STATE_HOME", dir); err != nil {
		auditEnvGate <- struct{}{}
		t.Fatalf("os.Setenv(XDG_STATE_HOME): %v", err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("XDG_STATE_HOME", prev)
		} else {
			_ = os.Unsetenv("XDG_STATE_HOME")
		}
		auditEnvGate <- struct{}{}
	})
	return dir
}

func TestSessionAuditLogsLifecycleEvents(t *testing.T) {
	t.Parallel()

	logger, buf := newAuditTestLogger()
	lockAuditStateHome(t)

	sess := newSession("audit-session")
	sess.logger = logger
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	pane1 := newTestPane(sess, 1, "pane-1")
	window := newTestWindowWithPanes(t, sess, 1, "window-1", pane1)
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.Panes = []*mux.Pane{pane1}
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.counter.Store(1)
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
	})

	pane2, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		pane := newTestPane(sess, 2, "pane-2")
		return pane, sess.insertPreparedPaneIntoActiveWindow(pane, mux.SplitHorizontal, false, false)
	})
	if err != nil {
		t.Fatalf("enqueueSessionQuery(insertPreparedPaneIntoActiveWindow): %v", err)
	}

	cc := &clientConn{ID: "client-1", cols: 80, rows: 24, inputIdle: true}
	mustSessionMutation(t, sess, func(sess *Session) {
		res := sess.handleAttachEvent(&Server{logger: logger}, cc, 80, 24)
		if res.err != nil {
			t.Fatalf("handleAttachEvent: %v", res.err)
		}
	})

	mustSessionMutation(t, sess, func(sess *Session) {
		sess.handleFinalizedPaneRemoval(pane2.ID, false, "exit 0")
	})

	pane3, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
		pane := newTestPane(sess, 3, "pane-3")
		return pane, sess.insertPreparedPaneIntoActiveWindow(pane, mux.SplitHorizontal, false, false)
	})
	if err != nil {
		t.Fatalf("enqueueSessionQuery(insertPreparedPaneIntoActiveWindow crash pane): %v", err)
	}
	mustSessionMutation(t, sess, func(sess *Session) {
		sess.handleFinalizedPaneRemoval(pane3.ID, false, "exit 2")
	})

	path, err := sess.writeCrashCheckpointNow()
	if err != nil {
		t.Fatalf("writeCrashCheckpointNow: %v", err)
	}
	if path == "" {
		t.Fatal("writeCrashCheckpointNow returned empty path")
	}

	cc.markDisconnectReason(disconnectReasonClosed)
	mustSessionMutation(t, sess, func(sess *Session) {
		(detachClientEvent{cc: cc, reason: DisconnectReasonSocketError}).handle(sess)
	})

	records := parseAuditRecords(t, buf)
	for _, event := range []string{"client_connect", "client_disconnect", "pane_create", "pane_exit", "pane_crash", "checkpoint_write"} {
		if record := findAuditRecord(records, event); record == nil {
			t.Fatalf("missing audit event %q in %v", event, records)
		}
	}
}

func TestHandleCommandAuditLogsCommandAndDuration(t *testing.T) {
	t.Parallel()

	logger, buf := newAuditTestLogger()

	sess := newSession("audit-command")
	sess.logger = logger
	defer stopSessionBackgroundLoops(t, sess)

	srv := &Server{
		logger:   logger,
		sessions: map[string]*Session{sess.Name: sess},
		commands: map[string]CommandHandler{
			"audit-ok": func(ctx *CommandContext) {
				ctx.reply("ok\n")
			},
		},
	}

	serverConn, peerConn := net.Pipe()
	defer serverConn.Close()
	defer peerConn.Close()

	cc := newClientConn(serverConn)
	cc.ID = "client-1"
	defer cc.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = ReadMsg(peerConn)
	}()

	commandDone := make(chan struct{})
	go func() {
		defer close(commandDone)
		cc.handleCommand(srv, sess, &Message{
			Type:    MsgTypeCommand,
			CmdName: "audit-ok",
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for command reply")
	}
	select {
	case <-commandDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for command handler completion")
	}

	record := findAuditRecord(parseAuditRecords(t, buf), "command_execute")
	if record == nil {
		t.Fatalf("missing command_execute audit record in %v", buf.String())
	}
	if record["command"] != "audit-ok" {
		t.Fatalf("command = %v, want audit-ok", record["command"])
	}
	if record["client_id"] != "client-1" {
		t.Fatalf("client_id = %v, want client-1", record["client_id"])
	}
	if _, ok := record["duration"]; !ok {
		t.Fatalf("duration missing from %v", record)
	}
}

func TestServerReloadAuditLogsHotReload(t *testing.T) {
	t.Parallel()

	logger, buf := newAuditTestLogger()
	lockAuditStateHome(t)

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-reload-audit-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	sess := newSession("reload-audit")
	sess.logger = logger
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	mustSessionMutation(t, sess, func(sess *Session) {
		pane := newTestPane(sess, 1, "pane-1")
		window := newTestWindowWithPanes(t, sess, 1, "window-1", pane)
		sess.Panes = []*mux.Pane{pane}
		sess.Windows = []*mux.Window{window}
		sess.ActiveWindowID = window.ID
		sess.counter.Store(1)
		sess.windowCounter.Store(1)
		sess.refreshInputTarget()
	})

	srv := &Server{
		listener:     listener,
		sessions:     map[string]*Session{sess.Name: sess},
		sockPath:     socketPath,
		logger:       logger,
		shutdownDone: make(chan struct{}),
	}
	sess.exitServer = srv

	if err := srv.Reload(filepath.Join(t.TempDir(), "missing-amux")); err == nil {
		t.Fatal("Reload() error = nil, want exec failure")
	}

	record := findAuditRecord(parseAuditRecords(t, buf), "hot_reload")
	if record == nil {
		t.Fatalf("missing hot_reload audit record in %v", buf.String())
	}
}

func TestCheckpointRestoreAuditLogsRestoreEvent(t *testing.T) {
	t.Parallel()

	logger, buf := newAuditTestLogger()
	lockAuditStateHome(t)

	sessionName := fmt.Sprintf("restore-audit-%d", time.Now().UnixNano())
	pane, layout := restoreTestLayout()
	cp := &ckpt.CrashCheckpoint{
		Version:       ckpt.CrashVersion,
		SessionName:   sessionName,
		WindowCounter: 1,
		Layout:        layout,
		PaneStates: []ckpt.CrashPaneState{{
			ID:        pane.ID,
			Meta:      pane.Meta,
			Cols:      80,
			Rows:      23,
			CreatedAt: time.Now(),
			IsProxy:   true,
		}},
		Timestamp: time.Now(),
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("amux-restore-audit-%d.sock", time.Now().UnixNano()))
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer os.Remove(socketPath)

	srv, err := newServerFromCrashCheckpointWithListenerLogger(sessionName, listener, socketPath, cp, "", mux.DefaultScrollbackLines, logger)
	if err != nil {
		t.Fatalf("newServerFromCrashCheckpointWithListenerLogger: %v", err)
	}
	defer srv.Shutdown()

	record := findAuditRecord(parseAuditRecords(t, buf), "checkpoint_restore")
	if record == nil {
		t.Fatalf("missing checkpoint_restore audit record in %v", buf.String())
	}
}
