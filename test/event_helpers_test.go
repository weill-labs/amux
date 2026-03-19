package test

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// eventJSON is a minimal struct for parsing event stream output.
type eventJSON struct {
	Type       string `json:"type"`
	Timestamp  string `json:"ts"`
	Generation uint64 `json:"generation,omitempty"`
	PaneID     uint32 `json:"pane_id,omitempty"`
	PaneName   string `json:"pane_name,omitempty"`
	Host       string `json:"host,omitempty"`
	ActivePane string `json:"active_pane,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	TimedOut   bool   `json:"-"` // set by readEvent on timeout
}

// eventStream connects to the server's events command and returns a scanner
// that reads one JSON event per line, plus a close function.
func eventStream(t *testing.T, session string, args ...string) (*bufio.Scanner, func()) {
	t.Helper()
	sockPath := server.SocketPath(session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: "events",
		CmdArgs: args,
	}); err != nil {
		conn.Close()
		t.Fatalf("write: %v", err)
	}

	pr, pw := net.Pipe()
	go func() {
		defer pw.Close()
		for {
			msg, err := server.ReadMsg(conn)
			if err != nil {
				return
			}
			if msg.CmdOutput != "" {
				pw.Write([]byte(msg.CmdOutput))
			}
		}
	}()

	scanner := bufio.NewScanner(pr)
	closer := func() {
		conn.Close()
		pr.Close()
	}
	return scanner, closer
}

// readEvent reads the next event from the scanner within timeout.
// Returns a zero eventJSON with TimedOut=true if the deadline expires.
func readEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	done := make(chan eventJSON, 1)
	go func() {
		if scanner.Scan() {
			var ev eventJSON
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				return
			}
			done <- ev
		}
	}()

	select {
	case ev := <-done:
		return ev
	case <-time.After(timeout):
		return eventJSON{TimedOut: true}
	}
}

// mustReadEvent reads the next event, fataling on timeout.
func mustReadEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	ev := readEvent(t, scanner, timeout)
	if ev.TimedOut {
		t.Fatal("timeout reading event")
	}
	return ev
}
