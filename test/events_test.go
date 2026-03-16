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

	cmdArgs := args
	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: "events",
		CmdArgs: cmdArgs,
	}); err != nil {
		conn.Close()
		t.Fatalf("write: %v", err)
	}

	// Read events by reading protocol messages and extracting CmdOutput
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

// readEvent reads the next event with a timeout.
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
		t.Fatal("timeout reading event")
		return eventJSON{}
	}
}

func TestEventsInitialSnapshot(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scanner, closer := eventStream(t, h.session)
	defer closer()

	// First event should be a layout snapshot
	ev := readEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("first event type: got %q, want %q", ev.Type, "layout")
	}
	if ev.Timestamp == "" {
		t.Error("event should have a timestamp")
	}
	if ev.ActivePane == "" {
		t.Error("layout event should have active_pane")
	}

	// Second event should be idle or busy for pane-1
	ev = readEvent(t, scanner, 5*time.Second)
	if ev.Type != "idle" && ev.Type != "busy" {
		t.Errorf("second event type: got %q, want idle or busy", ev.Type)
	}
	if ev.PaneName != "pane-1" {
		t.Errorf("pane name: got %q, want %q", ev.PaneName, "pane-1")
	}
}

func TestEventsLayoutOnSplit(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	scanner, closer := eventStream(t, h.session, "--filter", "layout")
	defer closer()

	// Drain initial layout snapshot
	readEvent(t, scanner, 5*time.Second)

	// Split should emit a layout event
	h.doSplit()

	ev := readEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("event type: got %q, want %q", ev.Type, "layout")
	}
	if ev.Generation == 0 {
		t.Error("layout event should have non-zero generation")
	}
}

func TestEventsFilterType(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Subscribe only to layout events
	scanner, closer := eventStream(t, h.session, "--filter", "layout")
	defer closer()

	// Drain initial layout snapshot
	readEvent(t, scanner, 5*time.Second)

	// Generate output (should NOT produce an event since we're filtered to layout)
	h.sendKeys("pane-1", "echo hello", "Enter")

	// Split SHOULD produce a layout event
	h.doSplit()

	ev := readEvent(t, scanner, 5*time.Second)
	if ev.Type != "layout" {
		t.Errorf("expected layout event, got %q", ev.Type)
	}
}

func TestEventsIdleBusyTransition(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	// Wait for pane to become idle first
	time.Sleep(server.DefaultIdleTimeout + 500*time.Millisecond)

	// Subscribe to idle and busy events for pane-1
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()

	// Drain initial snapshot (should be idle since we waited)
	ev := readEvent(t, scanner, 5*time.Second)
	if ev.Type != "idle" {
		t.Errorf("initial state: got %q, want idle", ev.Type)
	}

	// Generate activity — should trigger busy transition
	h.sendKeys("pane-1", "echo activity", "Enter")

	ev = readEvent(t, scanner, 5*time.Second)
	if ev.Type != "busy" {
		t.Errorf("after activity: got %q, want busy", ev.Type)
	}

	// Wait for idle timeout — should trigger idle transition
	ev = readEvent(t, scanner, server.DefaultIdleTimeout+3*time.Second)
	if ev.Type != "idle" {
		t.Errorf("after quiet: got %q, want idle", ev.Type)
	}
}

func TestEventsFilterPane(t *testing.T) {
	t.Parallel()
	h := newServerHarness(t)

	h.splitV() // creates pane-2

	// Wait for both panes to become idle
	time.Sleep(server.DefaultIdleTimeout + 500*time.Millisecond)

	// Subscribe only to pane-1 events
	scanner, closer := eventStream(t, h.session, "--filter", "idle,busy", "--pane", "pane-1")
	defer closer()

	// Drain initial snapshot (idle for pane-1)
	ev := readEvent(t, scanner, 5*time.Second)
	if ev.PaneName != "pane-1" {
		t.Errorf("initial event pane: got %q, want pane-1", ev.PaneName)
	}

	// Activity on pane-2 should NOT appear in pane-1's stream
	h.sendKeys("pane-2", "echo pane2activity", "Enter")

	// Activity on pane-1 SHOULD appear
	h.sendKeys("pane-1", "echo pane1activity", "Enter")

	ev = readEvent(t, scanner, 5*time.Second)
	if ev.PaneName != "pane-1" {
		t.Errorf("filtered event should be for pane-1, got %q", ev.PaneName)
	}
}
