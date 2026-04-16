package client

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/weill-labs/amux/internal/mouse"
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
	if !isTimeoutNetError(err) {
		t.Fatalf("read error = %v, want timeout", err)
	}
}

func TestSyncTerminalSizeSendsResizeWhenSizeChanges(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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

	run := func(doneReady, reloadReady bool) bool {
		done := make(chan struct{})
		if doneReady {
			close(done)
		}
		triggerReload := make(chan struct{}, 1)
		if reloadReady {
			triggerReload <- struct{}{}
		}
		return waitForRunSessionEnd(done, triggerReload)
	}

	t.Run("done without reload returns", func(t *testing.T) {
		t.Parallel()

		if run(true, false) {
			t.Fatal("reload should not run when only done is ready")
		}
	})

	t.Run("reload without done triggers reload", func(t *testing.T) {
		t.Parallel()

		if !run(false, true) {
			t.Fatal("reload should run when reload is ready")
		}
	})

	t.Run("done and reload both ready still triggers reload", func(t *testing.T) {
		t.Parallel()

		for i := 0; i < 1000; i++ {
			if !run(true, true) {
				t.Fatalf("iteration %d: reload should win when done and reload are both ready", i)
			}
		}
	})
}

func TestFormatAttachError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		err          error
		wantContains string
	}{
		{name: "protocol error", err: fmt.Errorf("%w: bad bootstrap", errAttachProtocol), wantContains: "attach failed: protocol error"},
		{name: "socket missing", err: os.ErrNotExist, wantContains: "attach failed: socket not found"},
		{name: "connection lost eof", err: io.EOF, wantContains: "attach failed: connection lost"},
		{
			name:         "ssh handshake keeps detail",
			err:          fmt.Errorf("SSH dial: ssh: handshake failed: %w", io.EOF),
			wantContains: "SSH dial: ssh: handshake failed",
		},
		{
			name:         "connection reset keeps detail",
			err:          errors.New("read unix /tmp/amux.sock->/tmp/amux.sock: connection reset by peer"),
			wantContains: "connection reset by peer",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := formatAttachError(tt.err)
			if err == nil || !strings.Contains(err.Error(), tt.wantContains) {
				t.Fatalf("formatAttachError(%v) = %v, want substring %q", tt.err, err, tt.wantContains)
			}
		})
	}
}

func TestFormatAttachErrorPreservesWrappedEOF(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("SSH dial: ssh: handshake failed: %w", io.EOF)

	got := formatAttachError(err)
	if !errors.Is(got, io.EOF) {
		t.Fatalf("errors.Is(%v, io.EOF) = false, want true", got)
	}
}

func TestIsConnectionLostError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "eof", err: io.EOF, want: true},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, want: true},
		{name: "wrapped eof", err: fmt.Errorf("ssh handshake: %w", io.EOF), want: true},
		{name: "closed network", err: net.ErrClosed, want: true},
		{name: "closed network text", err: errors.New("use of closed network connection"), want: true},
		{name: "broken pipe text", err: errors.New("write unix /tmp/amux.sock: broken pipe"), want: true},
		{name: "connection reset text", err: errors.New("connection reset by peer"), want: true},
		{name: "other error", err: errors.New("permission denied"), want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isConnectionLostError(tt.err); got != tt.want {
				t.Fatalf("isConnectionLostError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestDisconnectNoticeForReadError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "connection lost eof", err: io.EOF, want: "detached: connection lost"},
		{
			name: "connection lost unexpected eof during decode",
			err:  fmt.Errorf("decoding message: %w", io.ErrUnexpectedEOF),
			want: "detached: connection lost",
		},
		{
			name: "protocol error includes detail",
			err:  errors.New("decoding message: unknown wire type 7"),
			want: "detached: protocol error: decoding message: unknown wire type 7",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := disconnectNoticeForReadError(tt.err); got != tt.want {
				t.Fatalf("disconnectNoticeForReadError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestHotReloadDetachNotice(t *testing.T) {
	t.Parallel()

	clientVersion := currentClientVersion()

	tests := []struct {
		name          string
		serverVersion string
		wantContains  string
	}{
		{name: "matching version", serverVersion: clientVersion, wantContains: "detached: server requested hot-reload"},
		{name: "mismatched version", serverVersion: clientVersion + "-other", wantContains: "detached: binary version mismatch"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := hotReloadDetachNotice(tt.serverVersion)
			if !strings.Contains(got, tt.wantContains) {
				t.Fatalf("hotReloadDetachNotice(%q) = %q, want substring %q", tt.serverVersion, got, tt.wantContains)
			}
		})
	}
}

func TestDispatchQueuedMouseInputChunksCoalescesConsecutiveDragMotions(t *testing.T) {
	t.Parallel()

	parser := &mouse.Parser{}
	chunks := [][]byte{
		[]byte(ansi.MouseSgr(0, 10, 0, false)),
		[]byte(ansi.MouseSgr(32, 11, 0, false)),
		[]byte(ansi.MouseSgr(32, 12, 0, false)),
		[]byte(ansi.MouseSgr(32, 20, 0, false)),
		[]byte(ansi.MouseSgr(0, 20, 0, true)),
	}

	var got []mouse.Event
	shouldExit := dispatchQueuedMouseInputChunks(
		parser,
		chunks,
		func() bool { return true },
		func(ev mouse.Event) { got = append(got, ev) },
		func([]byte) bool { return false },
	)
	if shouldExit {
		t.Fatal("dispatchQueuedMouseInputChunks should not request exit")
	}
	if len(got) != 3 {
		t.Fatalf("mouse events = %d, want 3 (press, last motion, release)", len(got))
	}
	if got[0].Action != mouse.Press {
		t.Fatalf("first event action = %v, want press", got[0].Action)
	}
	if got[1].Action != mouse.Motion {
		t.Fatalf("second event action = %v, want motion", got[1].Action)
	}
	if got[2].Action != mouse.Release {
		t.Fatalf("third event action = %v, want release", got[2].Action)
	}
	if got[1].X != 20 || got[1].Y != 0 {
		t.Fatalf("coalesced motion = (%d,%d), want (20,0)", got[1].X, got[1].Y)
	}
	if got[1].LastX != 10 || got[1].LastY != 0 {
		t.Fatalf("coalesced motion last = (%d,%d), want press origin (10,0)", got[1].LastX, got[1].LastY)
	}
}

func TestDispatchQueuedMouseInputChunksKeepsAllMotionsOutsideDrag(t *testing.T) {
	t.Parallel()

	parser := &mouse.Parser{}
	chunks := [][]byte{
		[]byte(ansi.MouseSgr(0, 10, 0, false)),
		[]byte(ansi.MouseSgr(32, 11, 0, false)),
		[]byte(ansi.MouseSgr(32, 12, 0, false)),
		[]byte(ansi.MouseSgr(0, 12, 0, true)),
	}

	var got []mouse.Event
	shouldExit := dispatchQueuedMouseInputChunks(
		parser,
		chunks,
		func() bool { return false },
		func(ev mouse.Event) { got = append(got, ev) },
		func([]byte) bool { return false },
	)
	if shouldExit {
		t.Fatal("dispatchQueuedMouseInputChunks should not request exit")
	}
	if len(got) != 4 {
		t.Fatalf("mouse events = %d, want 4 when drag coalescing is disabled", len(got))
	}
}

func TestShouldBatchQueuedMouseInput(t *testing.T) {
	t.Parallel()

	parser := &mouse.Parser{}
	mousePress := []byte(ansi.MouseSgr(0, 10, 0, false))

	tests := []struct {
		name string
		raw  []byte
		drag dragState
		want bool
	}{
		{
			name: "drag already active keeps batching non-mouse reads",
			raw:  []byte("k"),
			drag: dragState{Active: true},
			want: true,
		},
		{
			name: "mouse-looking read batches before drag state flips",
			raw:  mousePress,
			want: true,
		},
		{
			name: "plain key input does not batch when idle",
			raw:  []byte("k"),
			want: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldBatchQueuedMouseInput(tt.raw, parser, &tt.drag); got != tt.want {
				t.Fatalf("shouldBatchQueuedMouseInput(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
