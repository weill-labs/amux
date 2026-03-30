package client

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
)

func readResizeMessage(t *testing.T, conn net.Conn) *proto.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	msg, err := proto.ReadMsg(conn)
	if err != nil {
		t.Fatalf("read resize message: %v", err)
	}
	return msg
}

func assertNoMessage(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	msg, err := proto.ReadMsg(conn)
	if err == nil {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("read error = %v, want timeout", err)
	}
}

func TestSyncTerminalSizeSendsResizeWhenSizeChanges(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})

	sender := newMessageSender(clientConn)
	t.Cleanup(sender.Close)

	cr := NewClientRenderer(80, 24)
	getSize := func(int) (int, int, error) {
		return 40, 12, nil
	}

	type sizeResult struct {
		cols int
		rows int
	}
	done := make(chan sizeResult, 1)
	go func() {
		cols, rows := syncTerminalSize(0, 80, 24, cr, sender, getSize, nil)
		done <- sizeResult{cols: cols, rows: rows}
	}()

	msg := readResizeMessage(t, serverConn)
	if msg.Type != proto.MsgTypeResize {
		t.Fatalf("message type = %d, want %d", msg.Type, proto.MsgTypeResize)
	}
	if msg.Cols != 40 || msg.Rows != 12 {
		t.Fatalf("resize message = %dx%d, want 40x12", msg.Cols, msg.Rows)
	}

	result := <-done
	if result.cols != 40 || result.rows != 12 {
		t.Fatalf("syncTerminalSize returned %dx%d, want 40x12", result.cols, result.rows)
	}

	snap := cr.renderer.snapshot()
	if snap.width != 40 || snap.height != 12 {
		t.Fatalf("renderer size = %dx%d, want 40x12", snap.width, snap.height)
	}
}

func TestSyncTerminalSizeSkipsUnchangedOrInvalidSizes(t *testing.T) {
	tests := []struct {
		name string
		fn   func(int) (int, int, error)
	}{
		{
			name: "unchanged",
			fn: func(int) (int, int, error) {
				return 80, 24, nil
			},
		},
		{
			name: "invalid dimensions",
			fn: func(int) (int, int, error) {
				return 0, 0, nil
			},
		},
		{
			name: "get size error",
			fn: func(int) (int, int, error) {
				return 0, 0, errors.New("boom")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			t.Cleanup(func() {
				_ = clientConn.Close()
				_ = serverConn.Close()
			})

			sender := newMessageSender(clientConn)
			t.Cleanup(sender.Close)

			cr := NewClientRenderer(80, 24)

			cols, rows := syncTerminalSize(0, 80, 24, cr, sender, tt.fn, nil)
			if cols != 80 || rows != 24 {
				t.Fatalf("syncTerminalSize returned %dx%d, want 80x24", cols, rows)
			}

			assertNoMessage(t, serverConn)

			snap := cr.renderer.snapshot()
			if snap.width != 80 || snap.height != 24 {
				t.Fatalf("renderer size = %dx%d, want 80x24", snap.width, snap.height)
			}
		})
	}
}

func TestTerminalEnterSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps proto.ClientCapabilities
		want string
	}{
		{
			name: "legacy",
			want: render.AltScreenEnter + render.MouseEnable + render.FocusEnable,
		},
		{
			name: "kitty keyboard",
			caps: proto.ClientCapabilities{KittyKeyboard: true},
			want: render.AltScreenEnter + render.MouseEnable + render.FocusEnable + render.KittyKeyboardEnable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalEnterSequence(tt.caps); got != tt.want {
				t.Fatalf("terminalEnterSequence() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTerminalExitSequence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps proto.ClientCapabilities
		want string
	}{
		{
			name: "legacy",
			want: render.FocusDisable + render.MouseDisable + render.AltScreenExit + render.ResetTitle,
		},
		{
			name: "kitty keyboard",
			caps: proto.ClientCapabilities{KittyKeyboard: true},
			want: render.KittyKeyboardDisable + render.FocusDisable + render.MouseDisable + render.AltScreenExit + render.ResetTitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := terminalExitSequence(tt.caps); got != tt.want {
				t.Fatalf("terminalExitSequence() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWaitForRunSessionEnd(t *testing.T) {
	t.Parallel()

	t.Run("done without reload returns", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{})
		close(done)
		triggerReload := make(chan struct{}, 1)
		reloaded := false

		waitForRunSessionEnd(done, triggerReload, func() {
			reloaded = true
		})

		if reloaded {
			t.Fatal("reload should not run when only done is ready")
		}
	})

	t.Run("reload without done triggers reload", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{})
		triggerReload := make(chan struct{}, 1)
		triggerReload <- struct{}{}
		reloaded := false

		waitForRunSessionEnd(done, triggerReload, func() {
			reloaded = true
		})

		if !reloaded {
			t.Fatal("reload should run when reload is ready")
		}
	})

	t.Run("done and reload both ready still triggers reload", func(t *testing.T) {
		t.Parallel()

		for i := 0; i < 1000; i++ {
			done := make(chan struct{})
			close(done)
			triggerReload := make(chan struct{}, 1)
			triggerReload <- struct{}{}
			reloaded := false

			waitForRunSessionEnd(done, triggerReload, func() {
				reloaded = true
			})

			if !reloaded {
				t.Fatalf("iteration %d: reload should win when done and reload are both ready", i)
			}
		}
	})
}
