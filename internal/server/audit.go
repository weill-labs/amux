package server

import (
	"fmt"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/mux"
)

func durationField(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.String()
}

func isPaneCrash(reason string) bool {
	switch reason {
	case "", "exit 0", "remote disconnect":
		return false
	default:
		return true
	}
}

func paneAuditFields(pane *mux.Pane) []any {
	if pane == nil {
		return nil
	}
	return []any{
		"pane_id", pane.ID,
		"pane_name", pane.Meta.Name,
		"host", pane.Meta.Host,
	}
}

func logWithLevel(logger *charmlog.Logger, level charmlog.Level, msg string, fields ...any) {
	if logger == nil {
		logger = auditlog.Discard()
	}
	switch level {
	case charmlog.DebugLevel:
		logger.Debug(msg, fields...)
	case charmlog.WarnLevel:
		logger.Warn(msg, fields...)
	case charmlog.ErrorLevel:
		logger.Error(msg, fields...)
	default:
		logger.Info(msg, fields...)
	}
}

func (s *Server) SetLogger(logger *charmlog.Logger) {
	if s == nil {
		return
	}
	if logger == nil {
		logger = auditlog.Discard()
	}
	s.logger = logger
	for sessionName, sess := range s.sessions {
		if sess == nil {
			continue
		}
		sess.logger = logger.With("session", sessionName)
		if setter, ok := any(sess.RemoteManager).(interface{ SetLogger(*charmlog.Logger) }); ok {
			setter.SetLogger(sess.logger.With("component", "ssh"))
		}
		if setter, ok := any(sess.remoteTakeover).(interface{ SetLogger(*charmlog.Logger) }); ok {
			setter.SetLogger(sess.logger.With("component", "ssh"))
		}
	}
}

func (s *Session) logClientConnect(cc *clientConn) {
	if s == nil || cc == nil {
		return
	}
	s.logger.Info("client connected",
		"event", "client_connect",
		"client_id", cc.ID,
		"cols", cc.cols,
		"rows", cc.rows,
		"interactive", !cc.nonInteractive,
	)
}

func (s *Session) logClientDisconnect(cc *clientConn, reason string) {
	if s == nil || cc == nil {
		return
	}
	s.logger.Info("client disconnected",
		"event", "client_disconnect",
		"client_id", cc.ID,
		"cols", cc.cols,
		"rows", cc.rows,
		"reason", reason,
	)
}

func (s *Session) logPaneCreate(pane *mux.Pane, source string) {
	if s == nil || pane == nil {
		return
	}
	fields := append([]any{"event", "pane_create"}, paneAuditFields(pane)...)
	fields = append(fields, "source", source, "remote", pane.IsProxy())
	s.logger.Info("pane created", fields...)
}

func (s *Session) logPaneExit(pane *mux.Pane, reason string) {
	if s == nil || pane == nil {
		return
	}
	fields := append([]any{"event", "pane_exit"}, paneAuditFields(pane)...)
	fields = append(fields, "reason", reason)
	s.logger.Info("pane exited", fields...)
	if isPaneCrash(reason) {
		crashFields := append([]any{"event", "pane_crash"}, paneAuditFields(pane)...)
		crashFields = append(crashFields, "reason", reason)
		s.logger.Error("pane crashed", crashFields...)
	}
}

func (s *Session) logCheckpointWrite(kind, path string, duration time.Duration, err error) {
	if s == nil {
		return
	}
	fields := []any{
		"event", "checkpoint_write",
		"checkpoint_kind", kind,
		"path", path,
		"duration", durationField(duration),
	}
	if err != nil {
		fields = append(fields, "error", err)
		s.logger.Warn("checkpoint write failed", fields...)
		return
	}
	s.logger.Info("checkpoint written", fields...)
}

func (s *Session) logCheckpointRestore(kind, path string, panes, windows int, duration time.Duration) {
	if s == nil {
		return
	}
	s.logger.Info("checkpoint restored",
		"event", "checkpoint_restore",
		"checkpoint_kind", kind,
		"path", path,
		"panes", panes,
		"windows", windows,
		"duration", durationField(duration),
	)
}

func (s *Session) logCommandExecution(clientID, command string, args []string, actorPaneID uint32, duration time.Duration, errMsg string) {
	if s == nil {
		return
	}
	fields := []any{
		"event", "command_execute",
		"client_id", clientID,
		"command", command,
		"args", args,
		"actor_pane_id", actorPaneID,
		"duration", durationField(duration),
	}
	if errMsg != "" {
		fields = append(fields, "error", errMsg)
		s.logger.Warn("command executed with error", fields...)
		return
	}
	s.logger.Info("command executed", fields...)
}

func (s *Session) logPanic(event string, recovered any, stack []byte) error {
	if s != nil {
		s.logger.Error("panic recovered",
			"event", event,
			"error", fmt.Sprint(recovered),
			"stack", string(stack),
		)
	}
	return fmt.Errorf("internal error: %v", recovered)
}
