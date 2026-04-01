package remote

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
)

func TestManagerSetLogger(t *testing.T) {
	t.Parallel()

	var nilManager *Manager
	nilManager.SetLogger(nil)

	host := &HostConn{}
	mgr := &Manager{
		hosts: map[string]*HostConn{
			"alpha": host,
			"nil":   nil,
		},
	}
	mgr.SetLogger(nil)
	if mgr.logger == nil {
		t.Fatal("mgr.logger is nil after SetLogger(nil)")
	}
	if host.logger == nil {
		t.Fatal("host.logger is nil after SetLogger(nil)")
	}
}

func TestRemoteAuditHelpers(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := auditlog.New(&buf, auditlog.Options{Format: auditlog.FormatJSON, Level: charmlog.DebugLevel})

	hc := &HostConn{
		name:         "alpha",
		connectAddr:  "alpha.example:22",
		sessionName:  "test-session",
		takeoverMode: true,
		localToRemote: map[uint32]uint32{
			1: 11,
			2: 22,
		},
		logger: logger,
	}
	hc.logSSHConnect()
	hc.logSSHDisconnect(charmlog.WarnLevel, "reconnect")
	logWithLevel(logger, charmlog.ErrorLevel, "ssh error", "event", "ssh_disconnect")

	mgr := &Manager{logger: logger}
	mgr.logDeployFailure("alpha", "deploy", errors.New("boom"))

	output := buf.String()
	for _, want := range []string{`"event":"ssh_connect"`, `"event":"ssh_disconnect"`, `"event":"ssh_deploy"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q missing %q", output, want)
		}
	}
}

func TestRemoteAuditHelpersHandleNil(t *testing.T) {
	t.Parallel()

	var nilHost *HostConn
	nilHost.logSSHConnect()
	nilHost.logSSHDisconnect(charmlog.InfoLevel, "closed")

	var nilManager *Manager
	nilManager.logDeployFailure("alpha", "deploy", errors.New("boom"))

	logWithLevel(nil, charmlog.InfoLevel, "ssh info", "event", "ssh_disconnect")
}
