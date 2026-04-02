package client

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime/coverage"
	"strings"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/muesli/termenv"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/copymode"
	"github.com/weill-labs/amux/internal/mouse"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/render"

	"golang.org/x/term"
)

type attachBootstrapMessage struct {
	typ     proto.MsgType
	paneID  uint32
	history []string
	data    []byte
}

func newAttachBootstrapMessage(msg *proto.Message) (attachBootstrapMessage, bool) {
	switch msg.Type {
	case proto.MsgTypePaneHistory:
		return attachBootstrapMessage{
			typ:     msg.Type,
			paneID:  msg.PaneID,
			history: append([]string(nil), msg.History...),
		}, true
	case proto.MsgTypePaneOutput:
		return attachBootstrapMessage{
			typ:    msg.Type,
			paneID: msg.PaneID,
			data:   append([]byte(nil), msg.PaneData...),
		}, true
	default:
		return attachBootstrapMessage{}, false
	}
}

func attachBootstrapPaneCount(layout *proto.LayoutSnapshot) int {
	if layout == nil {
		return 0
	}
	if len(layout.Windows) == 0 {
		return len(layout.Panes)
	}
	count := 0
	for _, ws := range layout.Windows {
		count += len(ws.Panes)
	}
	return count
}

func applyAttachBootstrapMessage(cr *ClientRenderer, msg attachBootstrapMessage) int {
	switch msg.typ {
	case proto.MsgTypePaneHistory:
		cr.HandlePaneHistory(msg.paneID, msg.history)
		return 0
	case proto.MsgTypePaneOutput:
		cr.HandlePaneOutput(msg.paneID, msg.data)
		return 1
	default:
		return 0
	}
}

func waitForRunSessionEnd(done <-chan struct{}, triggerReload <-chan struct{}, reload func()) {
	select {
	case <-done:
		select {
		case <-triggerReload:
			reload()
		default:
		}
	case <-triggerReload:
		reload()
	}
}

func readImmediateAttachCorrection(conn net.Conn, cr *ClientRenderer, timeout time.Duration) error {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil
			}
			return err
		}
		if msg.Type == proto.MsgTypeLayout {
			cr.HandleLayout(msg.Layout)
			continue
		}
		bufferedMsg, ok := newAttachBootstrapMessage(msg)
		if !ok {
			// Unknown message types (bell, copy-mode, etc.) end the correction
			// window without failing attach. Later layout or pane-output updates
			// still flow through the normal message loop.
			return nil
		}
		applyAttachBootstrapMessage(cr, bufferedMsg)
	}
}

func readAttachBootstrapPaneReplays(conn net.Conn, cr *ClientRenderer, remainingOutputs int, timeout time.Duration) (int, error) {
	if remainingOutputs <= 0 {
		return 0, nil
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return remainingOutputs, err
	}
	defer conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best-effort reset
	for remainingOutputs > 0 {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return remainingOutputs, nil
			}
			return remainingOutputs, err
		}
		if msg.Type == proto.MsgTypeLayout {
			cr.HandleLayout(msg.Layout)
			continue
		}
		bufferedMsg, ok := newAttachBootstrapMessage(msg)
		if !ok {
			// Unknown message types during pane replay should not fail attach.
			// Exit bootstrap early and let later state continue via the normal loop.
			return remainingOutputs, nil
		}
		remainingOutputs -= applyAttachBootstrapMessage(cr, bufferedMsg)
	}
	return 0, nil
}

func readAttachBootstrap(conn net.Conn, cr *ClientRenderer) error {
	var layout *proto.LayoutSnapshot
	var buffered []attachBootstrapMessage

	for layout == nil {
		msg, err := proto.ReadMsg(conn)
		if err != nil {
			return err
		}
		switch msg.Type {
		case proto.MsgTypeLayout:
			layout = msg.Layout
		default:
			bufferedMsg, ok := newAttachBootstrapMessage(msg)
			if !ok {
				return fmt.Errorf("unexpected attach bootstrap message type %d before layout", msg.Type)
			}
			buffered = append(buffered, bufferedMsg)
		}
	}

	cr.HandleLayout(layout)

	remainingOutputs := attachBootstrapPaneCount(layout)
	for _, msg := range buffered {
		remainingOutputs -= applyAttachBootstrapMessage(cr, msg)
	}

	remainingOutputs, err := readAttachBootstrapPaneReplays(conn, cr, remainingOutputs, config.BootstrapPaneReplayWait)
	if err != nil {
		return err
	}
	if remainingOutputs > 0 {
		// A stuck pane replay should not keep the whole terminal black forever.
		// Any late pane-output or layout messages are still applied by the
		// normal message loop after the initial frame is rendered.
		return nil
	}

	return readImmediateAttachCorrection(conn, cr, config.BootstrapCorrectionWindow)
}

type displayPaneSelectionResult struct {
	paneID uint32
	ok     bool
}

type chooserInputResult struct {
	action  chooserCommand
	handled bool
}

func toggleDisplayPanesOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if cr.DisplayPanesActive() {
			cr.HideDisplayPanes()
			return localRenderResult{
				effects: overlayRenderNowResult().effects,
				value:   true,
			}
		}
		if !cr.ShowDisplayPanes() {
			return localRenderResult{value: false}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}

func showChooserOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg, mode chooserMode) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.ShowChooser(mode) {
			return localRenderResult{value: false}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}

func showWindowRenamePromptOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg) bool {
	return callLocalRenderAction[bool](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.ShowWindowRenamePrompt() {
			return localRenderResult{value: false}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   true,
		}
	})
}

func handleChooserInputOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg, raw []byte) chooserInputResult {
	return callLocalRenderAction[chooserInputResult](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.ChooserActive() {
			return localRenderResult{value: chooserInputResult{}}
		}
		action := cr.HandleChooserInput(raw)
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value: chooserInputResult{
				action:  action,
				handled: true,
			},
		}
	})
}

func handleWindowRenamePromptInputOnRenderLoop(cr *ClientRenderer, msgCh chan<- *RenderMsg, raw []byte) promptCommand {
	return callLocalRenderAction[promptCommand](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		if !cr.WindowRenamePromptActive() {
			return localRenderResult{value: promptCommand{}}
		}
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   cr.HandleWindowRenamePromptInput(raw),
		}
	})
}

func handleDisplayPaneSelection(cr *ClientRenderer, sender *messageSender, b byte, msgCh chan<- *RenderMsg) {
	result := callLocalRenderAction[displayPaneSelectionResult](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		paneID, ok := cr.ResolveDisplayPaneKey(b)
		cr.HideDisplayPanes()
		return localRenderResult{
			effects: overlayRenderNowResult().effects,
			value:   displayPaneSelectionResult{paneID: paneID, ok: ok},
		}
	})
	if result.ok {
		sender.Command("focus", []string{fmt.Sprintf("%d", result.paneID)})
	}
}

func syncTerminalSize(fd int, prevCols, prevRows int, cr *ClientRenderer, sender *messageSender, getSize func(int) (int, int, error), msgCh chan<- *RenderMsg) (int, int) {
	c, r, _ := getSize(fd)
	if c <= 0 || r <= 0 {
		return prevCols, prevRows
	}
	if c == prevCols && r == prevRows {
		return prevCols, prevRows
	}
	_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
		cr.Resize(c, r)
		return localRenderResult{}
	})
	_ = sender.Send(&proto.Message{
		Type: proto.MsgTypeResize,
		Cols: c,
		Rows: r,
	})
	return c, r
}

func terminalEnterSequence(caps proto.ClientCapabilities) string {
	var b strings.Builder
	b.WriteString(render.AltScreenEnter)
	b.WriteString(render.MouseEnable)
	b.WriteString(render.FocusEnable)
	if caps.KittyKeyboard {
		b.WriteString(render.KittyKeyboardEnable)
	}
	return b.String()
}

func terminalExitSequence(caps proto.ClientCapabilities) string {
	var b strings.Builder
	if caps.KittyKeyboard {
		b.WriteString(render.KittyKeyboardDisable)
	}
	b.WriteString(render.FocusDisable)
	b.WriteString(render.MouseDisable)
	b.WriteString(render.AltScreenExit)
	b.WriteString(render.ResetTitle)
	return b.String()
}

// RunSession connects to an existing server or starts one, then enters raw
// terminal mode for interactive use.
func RunSession(sessionName string, getTermSize func(int) (int, int, error)) error {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	kb := config.DefaultKeybindings()
	scrollbackLines := cfg.EffectiveScrollbackLines()

	sockPath := proto.SocketPath(sessionName)

	if err := proto.EnsureDaemon(sessionName, 5*time.Second); err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer conn.Close()
	sender := newMessageSender(conn)
	defer sender.Close()

	fd := int(os.Stdin.Fd())
	cols, rows, _ := getTermSize(fd)
	if cols <= 0 {
		cols = proto.DefaultTermCols
	}
	if rows <= 0 {
		rows = proto.DefaultTermRows
	}

	// Send attach
	attachCaps := advertisedAttachCapabilities()
	negotiatedAttachCaps := proto.NegotiateClientCapabilities(attachCaps)
	attachProfile := attachColorProfile(os.Stdout, processEnviron{}, termenv.WithTTY(true))
	if err := sender.Send(&proto.Message{
		Type:               proto.MsgTypeAttach,
		Session:            sessionName,
		Cols:               cols,
		Rows:               rows,
		AttachMode:         proto.AttachModeInteractive,
		AttachColorProfile: attachProfile,
		AttachCapabilities: attachCaps,
	}); err != nil {
		return fmt.Errorf("sending attach: %w", err)
	}

	// Client-side renderer with per-pane emulators
	cr := newAttachClientRenderer(cols, rows, scrollbackLines, os.Stdout, processEnviron{}, termenv.WithTTY(true))
	cr.SetCapabilities(negotiatedAttachCaps)
	cr.OnUIEvent = func(name string) {
		_ = sender.Send(&proto.Message{
			Type:    proto.MsgTypeUIEvent,
			UIEvent: name,
		})
	}
	if err := readAttachBootstrap(conn, cr); err != nil {
		return fmt.Errorf("reading attach bootstrap: %w", err)
	}

	// Enter raw mode + alternate screen only once there is enough bootstrap
	// state to draw a real frame. If the server stalls before that point, keep
	// the user's current terminal visible instead of blanking it first.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	os.Stdout.Write([]byte(terminalEnterSequence(negotiatedAttachCaps)))
	defer func() {
		os.Stdout.Write([]byte(terminalExitSequence(negotiatedAttachCaps)))
		term.Restore(fd, oldState)
	}()

	if initial := cr.Render(true); initial != "" {
		io.WriteString(os.Stdout, initial)
	}

	// Resolve the current binary once so explicit reloads and server reload
	// notices can re-exec the client into the replacement binary.
	triggerReload := make(chan struct{}, 1)
	execPath, _ := reload.ResolveExecutable()

	// Channel for injecting keystrokes from type-keys (server → client).
	type injectedInput struct {
		data   []byte
		paneID uint32
	}
	injectCh := make(chan injectedInput, 16)

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
			case proto.MsgTypePaneHistory:
				cr.HandlePaneHistory(msg.PaneID, msg.History)
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
				resp := callLocalRenderAction[*proto.Message](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
					return localRenderResult{value: cr.HandleCaptureRequest(msg.CmdArgs, msg.AgentStatus)}
				})
				sender.Send(resp)
			case proto.MsgTypeTypeKeys:
				select {
				case injectCh <- injectedInput{data: msg.Input, paneID: msg.PaneID}:
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

	// Forward SIGWINCH to server and update client renderer.
	// The render loop is live before we start queueing local resize actions.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	defer signal.Stop(sigCh)
	initCols, initRows := cols, rows
	go func() {
		lastCols, lastRows := initCols, initRows
		for range sigCh {
			lastCols, lastRows = syncTerminalSize(fd, lastCols, lastRows, cr, sender, getTermSize, msgCh)
		}
	}()
	// Recheck once after the handler is live so startup-time size changes
	// (common on mobile/SSH clients) are not lost before the first SIGWINCH.
	cols, rows = syncTerminalSize(fd, cols, rows, cr, sender, getTermSize, msgCh)

	// Terminal → server: read input with mouse parsing + Ctrl-a prefix handling
	go func() {
		buf := make([]byte, 4096)
		prefix := false
		prefixEsc := false      // true after Ctrl-a then \x1b
		var prefixEscBuf []byte // buffered bytes after the \x1b
		altEsc := false         // true after bare \x1b (for alt+hjkl)
		mouseParser := &mouse.Parser{}

		// Mouse drag state — caches border direction from initial press
		var drag dragState

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
				if !showChooserOnRenderLoop(cr, msgCh, mode) {
					io.WriteString(os.Stdout, "\a")
					return
				}
			}
			showPrefixMessage := func(key byte) {
				cr.ShowPrefixMessage(formatUnboundPrefixMessage(kb.Prefix, key))
				io.WriteString(os.Stdout, "\a")
				runLocalRenderAction(cr, msgCh, func(*ClientRenderer) localRenderResult { return overlayRenderNowResult() })
			}
			clearPrefixMessage := func() {
				if !cr.ClearPrefixMessage() {
					return
				}
				runLocalRenderAction(cr, msgCh, func(*ClientRenderer) localRenderResult { return overlayRenderNowResult() })
			}

			// Pressing the prefix key again sends the literal prefix byte
			if b == kb.Prefix {
				clearPrefixMessage()
				*forward = append(*forward, kb.Prefix)
				return false
			}

			// Look up binding in dispatch table
			if binding, ok := kb.Bindings[b]; ok {
				clearPrefixMessage()
				switch binding.Action {
				case "detach":
					if len(*forward) > 0 {
						_ = sender.SendAsync(&proto.Message{
							Type: proto.MsgTypeInput, Input: *forward,
						})
					}
					sender.Send(&proto.Message{Type: proto.MsgTypeDetach})
					conn.Close()
					return true
				case "reload":
					if len(*forward) > 0 {
						_ = sender.SendAsync(&proto.Message{
							Type: proto.MsgTypeInput, Input: *forward,
						})
						*forward = nil
					}
					select {
					case triggerReload <- struct{}{}:
					default:
					}
				case "copy-mode":
					_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
						cr.EnterCopyMode(cr.ActivePaneID())
						return renderNowResult()
					})
				case "display-panes":
					if !toggleDisplayPanesOnRenderLoop(cr, msgCh) {
						io.WriteString(os.Stdout, "\a")
					}
				case "help":
					if !toggleHelpBarOnRenderLoop(cr, msgCh, kb) {
						io.WriteString(os.Stdout, "\a")
					}
				case "choose-tree":
					showChooser(chooserModeTree)
				case "choose-window":
					showChooser(chooserModeWindow)
				case "rename-window":
					if !showWindowRenamePromptOnRenderLoop(cr, msgCh) {
						io.WriteString(os.Stdout, "\a")
					}
				case "split":
					handleSplitBinding(cr, sender, binding, os.Stdout)
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
					_ = sender.SendAsync(&proto.Message{
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

		shouldHandleKeyLocally := func(key byte) bool {
			if prefix || repeatKey != 0 {
				return true
			}
			if key == kb.Prefix {
				return true
			}
			_, ok := altHJKL[key]
			return altEsc && ok
		}

		processKeyBytes := func(data []byte, forward *[]byte) bool {
			for _, b := range data {
				if processKeyByte(b, forward) {
					return true
				}
			}
			return false
		}

		shouldHandleDecodedKeyLocally := func(key uv.KeyPressEvent) bool {
			if prefix || repeatKey != 0 {
				return true
			}
			if keyPressMatchesByte(key, kb.Prefix) {
				return true
			}
			return key.MatchString("alt+h", "alt+j", "alt+k", "alt+l")
		}

		// Read stdin in a dedicated goroutine, sending chunks on stdinCh.
		// This allows the main input loop to select between stdin and
		// injected keystrokes from type-keys.
		stdin := os.Stdin
		stdinCh := make(chan []byte, 4)
		go func() {
			defer close(stdinCh)
			for {
				n, err := stdin.Read(buf)
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
			injectedPaneID := uint32(0)
			localInput := false
			localActivity := false
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
			case injected := <-injectCh:
				raw = injected.data
				injectedPaneID = injected.paneID
			case <-wordCopyTimer:
				if drag.PendingWordCopyPaneID != 0 {
					_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
						cr.CopyModeCopySelection(drag.PendingWordCopyPaneID)
						return renderNowResult()
					})
					drag.PendingWordCopyPaneID = 0
					drag.PendingWordCopyAt = time.Time{}
					drag.ClickCount = 0
				}
				continue
			}

			if localInput {
				localActivity = hasActivityInput(raw)
				if localActivity {
					cr.MarkLocalInput()
				}
			}

			inputActivity := !localInput || localActivity
			if inputActivity {
				cr.SetInputIdle(false)
			}

			if localInput && !localActivity {
				for _, decoded := range decodeInputEvents(raw) {
					if uiEvent, handled := clientUIEventForDecodedInput(decoded); handled && uiEvent != "" {
						sender.Send(&proto.Message{
							Type:    proto.MsgTypeUIEvent,
							UIEvent: uiEvent,
						})
					}
				}
				continue
			}

			if localActivity && cr.ClearCommandFeedback() {
				runLocalRenderAction(cr, msgCh, func(*ClientRenderer) localRenderResult { return overlayRenderNowResult() })
			}

			var forward []byte
			var copyInput []byte
			shouldExit := false

			sendForward := func(data []byte) {
				if len(data) == 0 {
					return
				}
				if injectedPaneID != 0 {
					_ = sender.SendAsync(&proto.Message{
						Type:     proto.MsgTypeInputPane,
						PaneID:   injectedPaneID,
						PaneData: data,
					})
					return
				}
				_ = sender.SendAsync(&proto.Message{
					Type:  proto.MsgTypeInput,
					Input: data,
				})
			}

			flushCopyInput := func() {
				if len(copyInput) == 0 {
					return
				}
				input := append([]byte(nil), copyInput...)
				copyInput = nil
				_ = callLocalRenderAction[struct{}](cr, msgCh, func(cr *ClientRenderer) localRenderResult {
					cm := cr.ActiveCopyMode()
					if cm == nil {
						return localRenderResult{}
					}
					action := cm.HandleInput(input)
					paneID := cr.ActivePaneID()
					switch action {
					case copymode.ActionExit:
						cr.ExitCopyMode(paneID)
					case copymode.ActionYank:
						cr.CopyModeCopySelection(paneID)
					}
					return renderNowResult()
				})
			}

			// dispatchDecoded routes one decoded input event through local
			// key handling, copy mode, or pane forwarding. Returns true if
			// the input goroutine should exit (detach).
			dispatchDecoded := func(decoded decodedInputEvent) bool {
				if uiEvent, handled := clientUIEventForDecodedInput(decoded); handled {
					if uiEvent != "" {
						sender.Send(&proto.Message{
							Type:    proto.MsgTypeUIEvent,
							UIEvent: uiEvent,
						})
					}
					return false
				}

				normalized := normalizeLocalInput(decoded.raw)
				if len(normalized) == 0 {
					normalized = decoded.raw
				}

				if cr.HelpBarActive() {
					dismissHelpBarOnRenderLoop(cr, msgCh)
					return false
				}

				if cr.DisplayPanesActive() {
					if len(normalized) > 0 {
						handleDisplayPaneSelection(cr, sender, normalized[0], msgCh)
					} else {
						cr.HideDisplayPanes()
						runLocalRenderAction(cr, msgCh, func(*ClientRenderer) localRenderResult { return overlayRenderNowResult() })
					}
					return false
				}

				if cr.WindowRenamePromptActive() {
					action := handleWindowRenamePromptInputOnRenderLoop(cr, msgCh, normalized)
					if action.bell {
						io.WriteString(os.Stdout, "\a")
					}
					if action.command != "" {
						sender.Command(action.command, action.args)
					}
					return false
				}

				if key, ok := decoded.event.(uv.KeyPressEvent); ok {
					if shouldHandleDecodedKeyLocally(key) {
						// Prefix/repeat bindings should still win while copy
						// mode is active, matching tmux behavior.
						flushCopyInput()
						return processKeyBytes(normalized, &forward)
					}
					if cr.ActiveCopyMode() != nil {
						copyInput = append(copyInput, normalized...)
						return false
					}
					forward = append(forward, forwardedBytesForDecodedInput(decoded)...)
					return false
				}

				if len(decoded.raw) == 1 && shouldHandleKeyLocally(decoded.raw[0]) {
					flushCopyInput()
					return processKeyByte(decoded.raw[0], &forward)
				}

				if cr.ActiveCopyMode() != nil {
					copyInput = append(copyInput, normalized...)
					return false
				}

				forward = append(forward, decoded.raw...)
				return false
			}

			// decodeAndDispatch decodes raw bytes into input events and
			// dispatches each through dispatchDecoded. Returns true if any
			// event triggers exit (detach).
			decodeAndDispatch := func(data []byte) bool {
				for _, decoded := range decodeInputEvents(data) {
					if dispatchDecoded(decoded) {
						return true
					}
				}
				return false
			}

			if cr.ChooserActive() {
				// ChooserActive() is an atomic snapshot read. Re-check on the
				// render loop before mutating chooser state so a queued layout can
				// hide the chooser first without racing the input goroutine.
				result := handleChooserInputOnRenderLoop(cr, msgCh, normalizeLocalInput(raw))
				if !result.handled {
					// If the chooser disappeared between the snapshot read above and
					// the render-loop action, drop this key instead of forwarding it
					// into the pane while the user was visually interacting with the
					// chooser.
					cr.SetInputIdle(true)
					continue
				}
				if result.action.bell {
					io.WriteString(os.Stdout, "\a")
				}
				if result.action.command != "" {
					sender.Command(result.action.command, result.action.args)
				}
				cr.SetInputIdle(true)
				continue
			}
			if cr.HelpBarActive() && !mouseParser.InputLooksLikeMouse(raw) {
				events := decodeInputEvents(raw)
				consumed := helpBarConsumedEvents(events, kb)
				dismissHelpBarOnRenderLoop(cr, msgCh)
				for _, decoded := range events[consumed:] {
					if dispatchDecoded(decoded) {
						shouldExit = true
						break
					}
				}
				flushCopyInput()
				sendForward(forward)
				if shouldExit {
					return
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
						sendForward(forward)
						forward = nil
					}
					handleMouseEvent(ev, cr, sender, &drag, msgCh)
					continue
				}

				shouldExit = decodeAndDispatch(flushed)
			}

			// Flush a standalone Escape at the end of a read so Esc then j
			// does not coalesce into Alt+j. Split CSI and mouse sequences
			// stay buffered in the parser and complete on the next read.
			if !shouldExit {
				shouldExit = decodeAndDispatch(mouseParser.FlushPending())
			}

			if shouldExit {
				if inputActivity {
					cr.SetInputIdle(true)
				}
				return
			}

			flushCopyInput()

			if len(forward) > 0 {
				sendForward(forward)
			}
			if inputActivity {
				cr.SetInputIdle(true)
			}
		}
	}()

	waitForRunSessionEnd(done, triggerReload, func() {
		if execPath != "" {
			ExecSelf(execPath, sender, fd, oldState, negotiatedAttachCaps)
		}
		// ExecSelf replaces the process; if we get here, exec failed fatally
	})
	return nil
}

func handleSplitBinding(cr *ClientRenderer, sender *messageSender, binding config.Binding, out io.Writer) {
	args, ok := splitBindingArgs(cr, binding)
	if ok {
		sender.Command(binding.Action, args)
		return
	}
	cr.ShowCommandError("cannot split: layout not ready yet")
	io.WriteString(out, "\a")
	if data := cr.RenderDiff(); data != "" {
		io.WriteString(out, data)
	}
}

func splitBindingArgs(cr *ClientRenderer, binding config.Binding) ([]string, bool) {
	args := append([]string(nil), binding.Args...)
	name := cr.ActivePaneName()
	if name == "" {
		return nil, false
	}
	return append([]string{name}, args...), true
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
func ExecSelf(execPath string, sender *messageSender, fd int, oldState *term.State, caps proto.ClientCapabilities) {
	// Pre-validate: binary must exist and be accessible
	if _, err := os.Stat(execPath); err != nil {
		return
	}

	// Clean disconnect from server
	sender.Send(&proto.Message{Type: proto.MsgTypeDetach})
	sender.conn.Close()

	// Restore terminal state
	term.Restore(fd, oldState)
	os.Stdout.Write([]byte(terminalExitSequence(caps)))

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
