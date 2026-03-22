package test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestConnectionLogCLI(t *testing.T) {
	t.Parallel()

	h := newServerHarness(t)
	gen := h.generation()
	conn := attachClientForConnectionLog(t, h.session, 90, 30)
	h.waitLayout(gen)

	gen = h.generation()
	if err := server.WriteMsg(conn, &server.Message{Type: server.MsgTypeDetach}); err != nil {
		t.Fatalf("WriteMsg detach: %v", err)
	}
	_ = conn.Close()
	h.waitLayout(gen)

	out := h.runCmd("connection-log")
	for _, want := range []string{
		"TS",
		"EVENT",
		"CLIENT",
		"COLS",
		"ROWS",
		"REASON",
		"client-1",
		"client-2",
		"attach",
		"detach",
		"90",
		"30",
		"client detach",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("connection-log missing %q:\n%s", want, out)
		}
	}
}

func attachClientForConnectionLog(t *testing.T, session string, cols, rows int) net.Conn {
	t.Helper()

	sockPath := server.SocketPath(session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeAttach,
		Session: session,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		_ = conn.Close()
		t.Fatalf("WriteMsg attach: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		t.Fatalf("SetReadDeadline: %v", err)
	}

	var layout *server.Message
	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			_ = conn.Close()
			t.Fatalf("ReadMsg initial layout: %v", err)
		}
		if msg.Type == server.MsgTypeLayout {
			layout = msg
			break
		}
	}

	replayed := 0
	wantReplays := len(layout.Layout.Panes)
	for {
		msg, err := server.ReadMsg(conn)
		if err != nil {
			_ = conn.Close()
			t.Fatalf("ReadMsg bootstrap: %v", err)
		}
		switch msg.Type {
		case server.MsgTypePaneHistory:
		case server.MsgTypePaneOutput:
			replayed++
			if replayed < wantReplays {
				continue
			}
		case server.MsgTypeLayout:
			if err := conn.SetReadDeadline(time.Time{}); err != nil {
				_ = conn.Close()
				t.Fatalf("reset deadline: %v", err)
			}
			return conn
		}
	}
}
