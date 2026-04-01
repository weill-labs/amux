package server

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/mux"
)

func TestDurationField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "negative clamps to zero", in: -time.Second, want: "0s"},
		{name: "positive preserved", in: 1500 * time.Millisecond, want: "1.5s"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := durationField(tt.in); got != tt.want {
				t.Fatalf("durationField(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsPaneCrash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		want   bool
	}{
		{name: "empty", reason: "", want: false},
		{name: "exit zero", reason: "exit 0", want: false},
		{name: "remote disconnect", reason: "remote disconnect", want: false},
		{name: "non zero exit", reason: "exit 1", want: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isPaneCrash(tt.reason); got != tt.want {
				t.Fatalf("isPaneCrash(%q) = %v, want %v", tt.reason, got, tt.want)
			}
		})
	}
}

func TestPaneAuditFields(t *testing.T) {
	t.Parallel()

	if got := paneAuditFields(nil); got != nil {
		t.Fatalf("paneAuditFields(nil) = %v, want nil", got)
	}

	pane := &mux.Pane{
		ID: 7,
		Meta: mux.PaneMeta{
			Name: "pane-7",
			Host: "remote-a",
		},
	}
	want := []any{"pane_id", uint32(7), "pane_name", "pane-7", "host", "remote-a"}
	if got := paneAuditFields(pane); !reflect.DeepEqual(got, want) {
		t.Fatalf("paneAuditFields() = %#v, want %#v", got, want)
	}
}

func TestAuditLogWithLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level charmlog.Level
		want  string
	}{
		{name: "debug", level: charmlog.DebugLevel, want: `"level":"debug"`},
		{name: "warn", level: charmlog.WarnLevel, want: `"level":"warn"`},
		{name: "error", level: charmlog.ErrorLevel, want: `"level":"error"`},
		{name: "default info", level: charmlog.InfoLevel, want: `"level":"info"`},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := auditlog.New(&buf, auditlog.Options{Format: auditlog.FormatJSON, Level: charmlog.DebugLevel})
			auditlog.LogWithLevel(logger, tt.level, "hello", "event", "audit_event")
			output := buf.String()
			if !strings.Contains(output, `"msg":"hello"`) || !strings.Contains(output, tt.want) {
				t.Fatalf("output %q missing expected content %q", output, tt.want)
			}
		})
	}
}

func TestAuditLogWithLevelNilLogger(t *testing.T) {
	t.Parallel()

	auditlog.LogWithLevel(nil, charmlog.InfoLevel, "hello", "event", "audit_event")
}

func TestServerSetLogger(t *testing.T) {
	t.Parallel()

	var nilServer *Server
	nilServer.SetLogger(nil)

	sess := newSession("audit-helper-session")
	stopCrashCheckpointLoop(t, sess)
	defer stopSessionBackgroundLoops(t, sess)

	srv := &Server{sessions: map[string]*Session{
		"nil-session": nil,
		sess.Name:     sess,
	}}
	srv.SetLogger(nil)
	if srv.logger == nil {
		t.Fatal("srv.logger is nil after SetLogger(nil)")
	}
	if sess.logger == nil {
		t.Fatal("sess.logger is nil after SetLogger(nil)")
	}
}
