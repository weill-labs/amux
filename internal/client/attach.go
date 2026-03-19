package client

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime/coverage"
	"strings"
	"syscall"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"

	"golang.org/x/term"
)

func handleDisplayPaneSelection(cr *ClientRenderer, sender *messageSender, b byte) {
	paneID, ok := cr.ResolveDisplayPaneKey(b)
	cr.HideDisplayPanes()
	if ok {
		sender.Command("focus", []string{fmt.Sprintf("%d", paneID)})
	}
}

// RunSession connects to an existing server or starts one, then enters raw
// terminal mode for interactive use.
func RunSession(sessionName string) error {
	// Load config for keybindings
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "amux: loading config: %v\n", err)
		cfg = &config.Config{}
	}
	kb, err := config.BuildKeybindings(&cfg.Keys)
	if err != nil {
		return fmt.Errorf("invalid keybindings: %w", err)
	}

	sockPath := server.SocketPath(sessionName)

	// Start server daemon if no socket exists
	if !server.SocketAlive(sockPath) {
		if err := server.StartDaemon(sessionName); err != nil {
			return fmt.Errorf("starting server: %w", err)
		}
		// Wait for socket to appear
		if err := server.WaitForSocket(sockPath, 5*time.Second); err != nil {
			return err
		}
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer conn.Close()
	sender := newMessageSender(conn)

	fd := int(os.Stdin.Fd())
	cols, rows, _ := term.GetSize(fd)
	if cols <= 0 {
		cols = server.DefaultTermCols
	}
	if rows <= 0 {
		rows = server.DefaultTermRows
	}

	// Send attach
	if err := sender.Send(&proto.Message{
		Type:    proto.MsgTypeAttach,
		Session: sessionName,
		Cols:    cols,
		Rows:    rows,
	}); err != nil {
		return fmt.Errorf("sending attach: %w", err)
	}

	// Enter raw mode + alternate screen buffer
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	os.Stdout.Write([]byte(render.AltScreenEnter))
	os.Stdout.Write([]byte(render.MouseEnable))
	defer func() {
		os.Stdout.Write([]byte(render.MouseDisable))
		os.Stdout.Write([]byte(render.AltScreenExit))
		os.Stdout.Write([]byte(render.ResetTitle))
		term.Restore(fd, oldState)
	}()

	// Client-side renderer with per-pane emulators
	cr := NewClientRenderer(cols, rows)
	cr.OnUIEvent = func(name string) {
		_ = sender.Send(&proto.Message{
			Type:    proto.MsgTypeUIEvent,
			UIEvent: name,
		})
	}

	// Hot reload: resolve binary path once, start file watcher.
	// AMUX_NO_WATCH=1 disables watching (used by test harness for the outer
	// client so only the inner client responds to binary changes).
	triggerReload := make(chan struct{}, 1)
	execPath, execErr := reload.ResolveExecutable()
	if execErr == nil && os.Getenv("AMUX_NO_WATCH") != "1" {
		go reload.WatchBinary(execPath, triggerReload, nil)
	}

	// Forward SIGWINCH to server and update client renderer
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			c, r, _ := term.GetSize(fd)
			if c > 0 && r > 0 {
				cr.Resize(c, r)
				sender.Send(&proto.Message{
					Type: proto.MsgTypeResize,
					Cols: c,
					Rows: r,
				})
			}
		}
	}()

	// Channel for injecting keystrokes from type-keys (server → client).
	injectCh := make(chan []byte, 16)

	// Server → client renderer → stdout
	// Messages are dispatched to a coalescing render loop that caps at ~60fps.
	done := make(chan struct{})
	msgCh := make(chan *RenderMsg, 256)

	// Read server messages and dispatch to render loop
	go func() {
		defer close(msgCh)
		for {
			msg, err := proto.ReadMsg(conn)
			if err != nil {
				return
			}
			switch msg.Type {
			case proto.MsgTypeLayout:
				msgCh <- &RenderMsg{Typ: RenderMsgLayout, Layout: msg.Layout}
			case proto.MsgTypePaneOutput:
				msgCh <- &RenderMsg{Typ: RenderMsgPaneOutput, PaneID: msg.PaneID, Data: msg.PaneData}
			case proto.MsgTypeCmdResult:
				if msg.CmdErr != "" {
					msgCh <- &RenderMsg{Typ: RenderMsgCmdError, Text: msg.CmdErr}
				}
			case proto.MsgTypeCopyMode:
				msgCh <- &RenderMsg{Typ: RenderMsgCopyMode, PaneID: msg.PaneID}
			case proto.MsgTypeExit:
				msgCh <- &RenderMsg{Typ: RenderMsgExit}
				return
			case proto.MsgTypeBell:
				msgCh <- &RenderMsg{Typ: RenderMsgBell}
			case proto.MsgTypeClipboard:
				msgCh <- &RenderMsg{Typ: RenderMsgClipboard, Data: msg.PaneData}
			case proto.MsgTypeCaptureRequest:
				// Server is forwarding a capture request — render from
				// client-side emulators and send the result back.
				resp := cr.HandleCaptureRequest(msg.CmdArgs, msg.AgentStatus)
				sender.Send(resp)
			case proto.MsgTypeTypeKeys:
				select {
				case injectCh <- msg.Input:
				default:
				}
			case proto.MsgTypeServerReload:
				// Server is reloading — re-exec ourselves to reconnect
				select {
				case triggerReload <- struct{}{}:
				default:
				}
				return
			}
		}
	}()

	// Coalescing render loop
	go func() {
		defer close(done)
		cr.RenderCoalesced(msgCh, func(data string) {
			io.WriteString(os.Stdout, data)
		})
	}()

	// Terminal → server: read input with mouse parsing + Ctrl-a prefix handling
	go func() {
		buf := make([]byte, 4096)
		prefix := false
		prefixEsc := false      // true after Ctrl-a then \x1b
		var prefixEscBuf []byte // buffered bytes after the \x1b
		altEsc := false         // true after bare \x1b (for alt+hjkl)
		mouseParser := &mouse.Parser{}

		// Mouse drag state — caches border direction from initial press
		var drag DragState

		// arrowDirection maps CSI final bytes to focus directions.
		arrowDirection := map[byte]string{
			'A': "up", 'B': "down", 'C': "right", 'D': "left",
		}

		// altHJKL maps alt+key bytes to focus directions.
		altHJKL := map[byte]string{
			'h': "left", 'j': "down", 'k': "up", 'l': "right",
		}

		// flushPrefixEsc forwards the buffered prefix+escape bytes as literal input.
		flushPrefixEsc := func(forward *[]byte) {
			prefixEsc = false
			*forward = append(*forward, 0x01, 0x1b)
			*forward = append(*forward, prefixEscBuf...)
			prefixEscBuf = nil
		}

		// Repeat key state — allows navigation/resize keys to repeat
		// without re-pressing the prefix, matching tmux's -r behavior.
		// Uses a deadline instead of a timer to avoid goroutine races.
		// AMUX_REPEAT_TIMEOUT overrides the default for tests.
		repeatTimeout := 500 * time.Millisecond
		if v := os.Getenv("AMUX_REPEAT_TIMEOUT"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				repeatTimeout = d
			}
		}
		var repeatKey byte
		var repeatDeadline time.Time

		// isRepeatableKey returns true for keys that can repeat without prefix.
		isRepeatableKey := func(b byte) bool {
			if binding, ok := kb.Bindings[b]; ok {
				switch binding.Action {
				case "focus", "resize-active":
					return true
				}
			}
			return false
		}

		// execPrefixKey executes a prefix keybinding via the config-driven
		// dispatch table. Returns true if the goroutine should exit (detach).
		execPrefixKey := func(b byte, forward *[]byte) bool {
			showChooser := func(mode chooserMode) {
				if !cr.ShowChooser(mode) {
					io.WriteString(os.Stdout, "\a")
					return
				}
				if data := cr.RenderDiff(); data != "" {
					io.WriteString(os.Stdout, data)
				}
			}
			showPrefixMessage := func(key byte) {
				cr.ShowPrefixMessage(formatUnboundPrefixMessage(kb.Prefix, key))
				io.WriteString(os.Stdout, "\a")
				if data := cr.RenderDiff(); data != "" {
					io.WriteString(os.Stdout, data)
				}
			}

			// Pressing the prefix key again sends the literal prefix byte
			if b == kb.Prefix {
				cr.ClearPrefixMessage()
				*forward = append(*forward, kb.Prefix)
				return false
			}

			// Look up binding in dispatch table
			if binding, ok := kb.Bindings[b]; ok {
				cr.ClearPrefixMessage()
				switch binding.Action {
				case "detach":
					if len(*forward) > 0 {
						sender.Send(&proto.Message{
							Type: proto.MsgTypeInput, Input: *forward,
						})
					}
					sender.Send(&proto.Message{Type: proto.MsgTypeDetach})
					conn.Close()
					return true
				case "reload":
					if len(*forward) > 0 {
						sender.Send(&proto.Message{
							Type: proto.MsgTypeInput, Input: *forward,
						})
						*forward = nil
					}
					select {
					case triggerReload <- struct{}{}:
					default:
					}
				case "copy-mode":
					cr.EnterCopyMode(cr.ActivePaneID())
					if data := cr.RenderDiff(); data != "" {
						io.WriteString(os.Stdout, data)
					}
				case "display-panes":
					if cr.DisplayPanesActive() {
						cr.HideDisplayPanes()
					} else if !cr.ShowDisplayPanes() {
						io.WriteString(os.Stdout, "\a")
						break
					}
					if data := cr.RenderDiff(); data != "" {
						io.WriteString(os.Stdout, data)
					}
				case "choose-tree":
					showChooser(chooserModeTree)
				case "choose-window":
					showChooser(chooserModeWindow)
				case "toggle-minimize":
					if reason := cr.toggleMinimizeBlockedReason(); reason != "" {
						cr.ShowCommandError(reason)
						io.WriteString(os.Stdout, "\a")
						if data := cr.RenderDiff(); data != "" {
							io.WriteString(os.Stdout, data)
						}
					}
					sender.Command(binding.Action, binding.Args)
				case "compat-bell":
					io.WriteString(os.Stdout, "\a")
				default:
					// Generic server command
					sender.Command(binding.Action, binding.Args)
				}
			} else if b == 0x1b {
				prefixEsc = true
				prefixEscBuf = nil
			} else {
				showPrefixMessage(b)
			}
			return false
		}

		// processKeyByte handles a single non-mouse byte through the
		// Ctrl-a prefix system. Returns true if the goroutine should exit.
		processKeyByte := func(b byte, forward *[]byte) bool {
			// Handle alt+hjkl: after a bare \x1b, check if next byte is h/j/k/l.
			if altEsc {
				altEsc = false
				if dir, ok := altHJKL[b]; ok {
					sender.Command("focus", []string{dir})
					return false
				}
				// Not alt+hjkl — forward the \x1b and process this byte normally.
				*forward = append(*forward, 0x1b)
				// Fall through to handle b via the rest of processKeyByte.
			}

			// Handle escape sequence buffering for prefix + arrow keys.
			// After Ctrl-a \x1b, we buffer bytes looking for CSI arrow: \x1b[A/B/C/D.
			if prefixEsc {
				prefixEscBuf = append(prefixEscBuf, b)
				if len(prefixEscBuf) == 1 && b == '[' {
					return false // waiting for direction byte
				}
				if len(prefixEscBuf) == 2 && prefixEscBuf[0] == '[' {
					if dir, ok := arrowDirection[b]; ok {
						prefixEsc = false
						prefixEscBuf = nil
						sender.Command("focus", []string{dir})
					} else {
						flushPrefixEsc(forward)
					}
					return false
				}
				flushPrefixEsc(forward)
				return false
			}

			// Repeat mode: any repeatable key executes without prefix while
			// the deadline hasn't expired. Matches tmux behavior where all
			// repeatable bindings stay active, not just the original key.
			if repeatKey != 0 {
				if isRepeatableKey(b) && time.Now().Before(repeatDeadline) {
					repeatKey = b
					repeatDeadline = time.Now().Add(repeatTimeout)
					return execPrefixKey(b, forward)
				}
				repeatKey = 0
			}

			if prefix {
				prefix = false
				if isRepeatableKey(b) {
					repeatKey = b
					repeatDeadline = time.Now().Add(repeatTimeout)
				}
				return execPrefixKey(b, forward)
			}

			if b == kb.Prefix {
				if len(*forward) > 0 {
					sender.Send(&proto.Message{
						Type: proto.MsgTypeInput, Input: *forward,
					})
					*forward = nil
				}
				prefix = true
				return false
			}

			if b == 0x1b {
				altEsc = true
				return false
			}

			*forward = append(*forward, b)
			return false
		}

		// Read stdin in a dedicated goroutine, sending chunks on stdinCh.
		// This allows the main input loop to select between stdin and
		// injected keystrokes from type-keys.
		stdinCh := make(chan []byte, 4)
		go func() {
			defer close(stdinCh)
			for {
				n, err := os.Stdin.Read(buf)
				if err != nil {
					return
				}
				cp := make([]byte, n)
				copy(cp, buf[:n])
				stdinCh <- cp
			}
		}()

		for {
			var raw []byte
			localInput := false
			var wordCopyTimer <-chan time.Time
			if !drag.PendingWordCopyAt.IsZero() {
				wait := time.Until(drag.PendingWordCopyAt)
				if wait < 0 {
					wait = 0
				}
				wordCopyTimer = time.After(wait)
			}
			select {
			case data, ok := <-stdinCh:
				if !ok {
					return
				}
				raw = data
				localInput = true
			case data := <-injectCh:
				raw = data
			case <-wordCopyTimer:
				if drag.PendingWordCopyPaneID != 0 {
					cr.CopyModeCopySelection(drag.PendingWordCopyPaneID)
					drag.PendingWordCopyPaneID = 0
					drag.PendingWordCopyAt = time.Time{}
					drag.ClickCount = 0
					if data := cr.RenderDiff(); data != "" {
						io.WriteString(os.Stdout, data)
					}
				}
				continue
			}
			cr.SetInputIdle(false)

			if localInput && cr.ClearCommandFeedback() {
				if data := cr.RenderDiff(); data != "" {
					io.WriteString(os.Stdout, data)
				}
			}

			var forward []byte
			var copyInput []byte
			shouldExit := false

			flushCopyInput := func() {
				if len(copyInput) == 0 {
					return
				}
				cm := cr.ActiveCopyMode()
				if cm == nil {
					copyInput = nil
					return
				}
				action := cm.HandleInput(copyInput)
				paneID := cr.ActivePaneID()
				copyInput = nil
				switch action {
				case copymode.ActionExit:
					cr.ExitCopyMode(paneID)
				case copymode.ActionYank:
					cr.CopyModeCopySelection(paneID)
				}
				if data := cr.RenderDiff(); data != "" {
					io.WriteString(os.Stdout, data)
				}
			}

			if cr.ChooserActive() {
				action := cr.HandleChooserInput(raw)
				if action.bell {
					io.WriteString(os.Stdout, "\a")
				}
				if data := cr.RenderDiff(); data != "" {
					io.WriteString(os.Stdout, data)
				}
				if action.command != "" {
					sender.Command(action.command, action.args)
				}
				cr.SetInputIdle(true)
				continue
			}
			for i := 0; i < len(raw) && !shouldExit; i++ {
				ev, isMouse, flushed := mouseParser.Feed(raw[i])

				if isMouse {
					flushCopyInput()
					// Flush any accumulated forward bytes before handling mouse
					if len(forward) > 0 {
						sender.Send(&proto.Message{
							Type: proto.MsgTypeInput, Input: forward,
						})
						forward = nil
					}
					HandleMouseEvent(ev, cr, sender, &drag)
					if cr.IsDirty() {
						if data := cr.RenderDiff(); data != "" {
							io.WriteString(os.Stdout, data)
						}
					}
					continue
				}

				// Process flushed bytes (normal input that passed through parser)
				for _, fb := range flushed {
					if cr.DisplayPanesActive() {
						handleDisplayPaneSelection(cr, sender, fb)
						if data := cr.RenderDiff(); data != "" {
							io.WriteString(os.Stdout, data)
						}
						continue
					}
					if cr.ActiveCopyMode() != nil {
						copyInput = append(copyInput, fb)
						continue
					}
					if processKeyByte(fb, &forward) {
						shouldExit = true
						break
					}
				}
			}

			if shouldExit {
				cr.SetInputIdle(true)
				return
			}

			if cr.ActiveCopyMode() != nil {
				copyInput = append(copyInput, mouseParser.FlushPending()...)
			}
			flushCopyInput()

			if len(forward) > 0 {
				sender.Send(&proto.Message{
					Type: proto.MsgTypeInput, Input: forward,
				})
			}
			cr.SetInputIdle(true)
		}
	}()

	// Wait for session end or hot reload trigger
	select {
	case <-done:
		return nil
	case <-triggerReload:
		if execPath != "" {
			ExecSelf(execPath, sender, fd, oldState)
		}
		// ExecSelf replaces the process; if we get here, exec failed fatally
		return nil
	}
}

var copyToClipboard = CopyToClipboard

// CopyToClipboard copies text to the system clipboard.
func CopyToClipboard(text string) {
	// Try pbcopy (macOS), then xclip (Linux), then xsel (Linux)
	for _, cmd := range [][]string{
		{"pbcopy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Stdin = strings.NewReader(text)
		if c.Run() == nil {
			return
		}
	}
}

func formatUnboundPrefixMessage(prefix, key byte) string {
	return "No binding for " + formatKeyName(prefix) + " " + formatKeyName(key)
}

func formatKeyName(b byte) string {
	if b >= 1 && b <= 26 {
		return "C-" + string(rune('a'+b-1))
	}
	switch b {
	case 0x1b:
		return "Esc"
	case ' ':
		return "Space"
	default:
		return string([]byte{b})
	}
}

// ExecSelf replaces the current process with the binary at execPath.
// Pre-validates the binary before tearing down the connection.
func ExecSelf(execPath string, sender *messageSender, fd int, oldState *term.State) {
	// Pre-validate: binary must exist and be accessible
	if _, err := os.Stat(execPath); err != nil {
		return
	}

	// Clean disconnect from server
	sender.Send(&proto.Message{Type: proto.MsgTypeDetach})
	sender.conn.Close()

	// Restore terminal state
	term.Restore(fd, oldState)
	os.Stdout.Write([]byte(render.MouseDisable))
	os.Stdout.Write([]byte(render.AltScreenExit))
	os.Stdout.Write([]byte(render.ResetTitle))

	// Flush coverage data before exec (which replaces the process image
	// without running atexit handlers). No-op if not built with -cover.
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		_ = coverage.WriteCountersDir(dir)
	}

	// Replace process
	err := syscall.Exec(execPath, os.Args, os.Environ())
	if err != nil {
		// Unrecoverable — connection is closed
		os.Stderr.WriteString("amux: reload failed: " + err.Error() + "\n")
		os.Exit(1)
	}
}
