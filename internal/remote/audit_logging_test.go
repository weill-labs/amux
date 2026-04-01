package remote

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"github.com/weill-labs/amux/internal/config"
)

func newRemoteAuditTestLogger() (*charmlog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := auditlog.New(&buf, auditlog.Options{
		Format: auditlog.FormatJSON,
		Level:  charmlog.DebugLevel,
	})
	return logger, &buf
}

func parseRemoteAuditRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
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

func countRemoteAuditEvents(records []map[string]any, event string) int {
	count := 0
	for _, record := range records {
		if record["event"] == event {
			count++
		}
	}
	return count
}

func TestHostConnAuditLogsSSHLifecycle(t *testing.T) {
	t.Parallel()

	logger, buf := newRemoteAuditTestLogger()

	hc := NewHostConn("gpu-box", config.Host{Address: "gpu-box:22"}, "hash", nil, nil, nil)
	hc.logger = logger
	defer hc.Close()

	clientConn1, serverConn1 := net.Pipe()
	defer serverConn1.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.state = Connecting
		(connectDoneEvent{outcome: &connectOutcome{
			amuxConn:    clientConn1,
			sessionName: "main@local",
			remoteUID:   "1000",
			connectAddr: "gpu-box:22",
		}}).handle(hc)
	})

	testInActor(hc, func(hc *HostConn) {
		(disconnectEvent{}).handle(hc)
	})

	clientConn2, serverConn2 := net.Pipe()
	defer serverConn2.Close()

	testInActor(hc, func(hc *HostConn) {
		hc.state = Connecting
		(connectDoneEvent{outcome: &connectOutcome{
			amuxConn:    clientConn2,
			sessionName: "main@local",
			remoteUID:   "1000",
			connectAddr: "gpu-box:22",
		}}).handle(hc)
	})

	testInActor(hc, func(hc *HostConn) {
		(readDisconnectEvent{}).handle(hc)
	})

	records := parseRemoteAuditRecords(t, buf)
	if got := countRemoteAuditEvents(records, "ssh_connect"); got < 2 {
		t.Fatalf("ssh_connect events = %d, want at least 2 in %v", got, records)
	}
	if got := countRemoteAuditEvents(records, "ssh_disconnect"); got < 2 {
		t.Fatalf("ssh_disconnect events = %d, want at least 2 in %v", got, records)
	}
}
