package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
)

// CommandHandler processes a single CLI command.
type CommandHandler func(ctx *CommandContext)

// CommandContext provides all state a command handler needs.
type CommandContext struct {
	CC   *ClientConn
	Srv  *Server
	Sess *Session
	Args []string
}

func (ctx *CommandContext) reply(output string) {
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: output})
}

func (ctx *CommandContext) replyErr(errMsg string) {
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdErr: errMsg})
}

// commandRegistry maps command names to their handlers, following
// tmux's pattern of one entry per command.
var commandRegistry = map[string]CommandHandler{
	"list":            cmdList,
	"split":           cmdSplit,
	"focus":           cmdFocus,
	"capture":         cmdCapture,
	"spawn":           cmdSpawn,
	"zoom":            cmdZoom,
	"minimize":        cmdMinimize,
	"restore":         cmdRestore,
	"toggle-minimize": cmdToggleMinimize,
	"kill":            cmdKill,
	"send-keys":       cmdSendKeys,
	"status":          cmdStatus,
	"new-window":      cmdNewWindow,
	"list-windows":    cmdListWindows,
	"select-window":   cmdSelectWindow,
	"next-window":     cmdNextWindow,
	"prev-window":     cmdPrevWindow,
	"rename-window":   cmdRenameWindow,
	"resize-border":   cmdResizeBorder,
	"resize-active":   cmdResizeActive,
	"resize-window":   cmdResizeWindow,
	"swap":            cmdSwap,
	"rotate":          cmdRotate,
	"copy-mode":       cmdCopyMode,
	"generation":      cmdGeneration,
	"wait-layout":     cmdWaitLayout,
	"clipboard-gen":   cmdClipboardGen,
	"wait-clipboard":  cmdWaitClipboard,
	"wait-for":        cmdWaitFor,
	"wait-idle":       cmdWaitIdle,
	"wait-busy":       cmdWaitBusy,
	"set-hook":        cmdSetHook,
	"unset-hook":      cmdUnsetHook,
	"list-hooks":      cmdListHooks,
	"events":          cmdEvents,
	"hosts":           cmdHosts,
	"disconnect":      cmdDisconnect,
	"reconnect":       cmdReconnect,
	"reload-server":   cmdReloadServer,
	"unsplice":        cmdUnsplice,
	"_inject-proxy":   cmdInjectProxy,
}

func cmdList(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	var output string
	if len(ctx.Sess.Panes) == 0 {
		output = "No panes.\n"
	} else {
		output = fmt.Sprintf("%-6s %-20s %-15s %-10s %s\n", "PANE", "NAME", "HOST", "WINDOW", "TASK")
		w := ctx.Sess.ActiveWindow()
		for _, p := range ctx.Sess.Panes {
			active := " "
			if w != nil && w.ActivePane != nil && w.ActivePane.ID == p.ID {
				active = "*"
			}
			winName := ""
			if pw := ctx.Sess.FindWindowByPaneID(p.ID); pw != nil {
				winName = pw.Name
			}
			output += fmt.Sprintf("%-6s %-20s %-15s %-10s %s\n",
				fmt.Sprintf("%s%d", active, p.ID),
				p.Meta.Name, p.Meta.Host, winName, p.Meta.Task)
		}
	}
	ctx.Sess.mu.Unlock()
	ctx.reply(output)
}

func cmdSplit(ctx *CommandContext) {
	rootLevel := false
	dir := mux.SplitHorizontal
	var hostName string
	for _, arg := range ctx.Args {
		switch arg {
		case "v":
			dir = mux.SplitVertical
		case "root":
			rootLevel = true
		}
	}
	for i := 0; i < len(ctx.Args)-1; i++ {
		if ctx.Args[i] == "--host" {
			hostName = ctx.Args[i+1]
		}
	}
	// If no --host flag, inherit the active pane's host when it's a
	// remote proxy pane. Splitting a remote pane should stay remote.
	if hostName == "" {
		ctx.Sess.mu.Lock()
		if w := ctx.Sess.ActiveWindow(); w != nil && w.ActivePane != nil && w.ActivePane.IsProxy() {
			hostName = w.ActivePane.Meta.Host
		}
		ctx.Sess.mu.Unlock()
	}

	if hostName != "" {
		pane := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, hostName, dir, rootLevel)
		if pane != nil {
			ctx.reply(fmt.Sprintf("Split %s: new remote pane %s @%s\n", dirName(dir), pane.Meta.Name, hostName))
		}
	} else {
		pane := ctx.CC.splitNewPane(ctx.Srv, ctx.Sess, mux.PaneMeta{}, dir, rootLevel)
		if pane != nil {
			ctx.reply(fmt.Sprintf("Split %s: new pane %s\n", dirName(dir), pane.Meta.Name))
		}
	}
}

func cmdFocus(ctx *CommandContext) {
	direction := "next"
	if len(ctx.Args) > 0 {
		direction = ctx.Args[0]
	}

	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		return
	}

	switch direction {
	case "next", "left", "right", "up", "down":
		w.Focus(direction)
		name := w.ActivePane.Meta.Name
		ctx.Sess.mu.Unlock()
		ctx.Sess.broadcastLayout()
		ctx.reply(fmt.Sprintf("Focused %s\n", name))
	default:
		pane := ctx.CC.resolvePaneAcrossWindows(ctx.Sess, "focus", direction)
		if pane == nil {
			ctx.Sess.mu.Unlock()
			return
		}
		if pw := ctx.Sess.FindWindowByPaneID(pane.ID); pw != nil {
			ctx.Sess.ActiveWindowID = pw.ID
			pw.FocusPane(pane)
		}
		ctx.Sess.mu.Unlock()
		ctx.Sess.broadcastLayout()
		ctx.reply(fmt.Sprintf("Focused %s\n", pane.Meta.Name))
	}
}

func cmdCapture(ctx *CommandContext) {
	result := ctx.Sess.forwardCapture(ctx.Args)
	ctx.CC.Send(result)
}

func cmdSpawn(ctx *CommandContext) {
	meta := mux.PaneMeta{Host: mux.DefaultHost}
	var remoteHost string
	for i := 0; i < len(ctx.Args)-1; i += 2 {
		switch ctx.Args[i] {
		case "--name":
			meta.Name = ctx.Args[i+1]
		case "--host":
			meta.Host = ctx.Args[i+1]
			remoteHost = ctx.Args[i+1]
		case "--task":
			meta.Task = ctx.Args[i+1]
		case "--color":
			meta.Color = ctx.Args[i+1]
		}
	}
	if meta.Name == "" {
		ctx.replyErr("--name is required")
		return
	}
	if remoteHost != "" && remoteHost != mux.DefaultHost {
		pane := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, remoteHost, mux.SplitHorizontal, false)
		if pane != nil {
			pane.Meta.Name = meta.Name
			if meta.Task != "" {
				pane.Meta.Task = meta.Task
			}
			ctx.Sess.broadcastLayout()
			ctx.reply(fmt.Sprintf("Spawned %s in pane %d @%s\n", meta.Name, pane.ID, remoteHost))
		}
	} else {
		pane := ctx.CC.splitNewPane(ctx.Srv, ctx.Sess, meta, mux.SplitHorizontal, false)
		if pane != nil {
			ctx.reply(fmt.Sprintf("Spawned %s in pane %d\n", meta.Name, pane.ID))
		}
	}
}

func cmdZoom(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no session")
		return
	}
	var pane *mux.Pane
	if len(ctx.Args) > 0 {
		pane = w.ResolvePane(ctx.Args[0])
		if pane == nil {
			ctx.Sess.mu.Unlock()
			ctx.replyErr(fmt.Sprintf("pane %q not found", ctx.Args[0]))
			return
		}
	} else {
		pane = w.ActivePane
	}
	if pane == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no active pane")
		return
	}
	willUnzoom := w.ZoomedPaneID == pane.ID
	err := w.Zoom(pane.ID)
	ctx.Sess.mu.Unlock()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcastLayout()
	verb := "Zoomed"
	if willUnzoom {
		verb = "Unzoomed"
	}
	ctx.reply(fmt.Sprintf("%s %s\n", verb, pane.Meta.Name))
}

func cmdMinimize(ctx *CommandContext) {
	ctx.CC.withPaneWindow(ctx.Sess, "minimize", ctx.Args, func(p *mux.Pane, w *mux.Window) (string, error) {
		return fmt.Sprintf("Minimized %s\n", p.Meta.Name), w.Minimize(p.ID)
	})
}

func cmdRestore(ctx *CommandContext) {
	ctx.CC.withPaneWindow(ctx.Sess, "restore", ctx.Args, func(p *mux.Pane, w *mux.Window) (string, error) {
		return fmt.Sprintf("Restored %s\n", p.Meta.Name), w.Restore(p.ID)
	})
}

func cmdToggleMinimize(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no active window")
		return
	}
	name, didMinimize, err := w.ToggleMinimize()
	ctx.Sess.mu.Unlock()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcastLayout()
	verb := "Restored"
	if didMinimize {
		verb = "Minimized"
	}
	ctx.reply(fmt.Sprintf("%s %s\n", verb, name))
}

func cmdKill(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	var pane *mux.Pane
	if len(ctx.Args) == 0 {
		w := ctx.Sess.ActiveWindow()
		if w != nil {
			pane = w.ActivePane
		}
	} else {
		pane = ctx.CC.resolvePane(ctx.Sess, "kill", ctx.Args)
	}
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	if len(ctx.Sess.Panes) <= 1 {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("cannot kill last pane")
		return
	}
	paneID := pane.ID
	paneName := pane.Meta.Name
	ctx.Sess.removePane(paneID)
	ctx.Sess.closePaneInWindow(paneID)
	ctx.Sess.mu.Unlock()
	pane.Close()

	ctx.Sess.broadcastLayout()
	ctx.reply(fmt.Sprintf("Killed %s\n", paneName))
}

func cmdSendKeys(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: send-keys <pane> [--hex] <keys>...")
		return
	}
	hexMode := false
	var keys []string
	for _, arg := range ctx.Args[1:] {
		if arg == "--hex" {
			hexMode = true
		} else {
			keys = append(keys, arg)
		}
	}
	ctx.Sess.mu.Lock()
	pane := ctx.CC.resolvePane(ctx.Sess, "send-keys", ctx.Args[:1])
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	var data []byte
	if hexMode {
		for _, hexStr := range keys {
			b, err := hex.DecodeString(hexStr)
			if err != nil {
				ctx.Sess.mu.Unlock()
				ctx.replyErr(fmt.Sprintf("invalid hex: %s", hexStr))
				return
			}
			data = append(data, b...)
		}
	} else {
		for _, key := range keys {
			data = append(data, parseKey(key)...)
		}
	}
	pane.Write(data)
	ctx.Sess.mu.Unlock()
	ctx.reply(fmt.Sprintf("Sent %d bytes to %s\n", len(data), pane.Meta.Name))
}

func cmdStatus(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	total := len(ctx.Sess.Panes)
	minimized := 0
	for _, p := range ctx.Sess.Panes {
		if p.Meta.Minimized {
			minimized++
		}
	}
	zoomed := ""
	w := ctx.Sess.ActiveWindow()
	if w != nil && w.ZoomedPaneID != 0 {
		for _, p := range ctx.Sess.Panes {
			if p.ID == w.ZoomedPaneID {
				zoomed = p.Meta.Name
				break
			}
		}
	}
	windowCount := len(ctx.Sess.Windows)
	ctx.Sess.mu.Unlock()
	active := total - minimized
	statusLine := fmt.Sprintf("windows: %d, panes: %d total, %d active, %d minimized", windowCount, total, active, minimized)
	if zoomed != "" {
		statusLine += fmt.Sprintf(", %s zoomed", zoomed)
	}
	if BuildVersion != "" {
		statusLine += fmt.Sprintf(", build: %s", BuildVersion)
	}
	ctx.reply(statusLine + "\n")
}

func cmdNewWindow(ctx *CommandContext) {
	var name string
	for i := 0; i < len(ctx.Args)-1; i += 2 {
		if ctx.Args[i] == "--name" {
			name = ctx.Args[i+1]
		}
	}
	ctx.CC.createNewWindow(ctx.Srv, ctx.Sess, name)
}

func cmdListWindows(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-6s %-20s %-8s\n", "WIN", "NAME", "PANES"))
	for i, w := range ctx.Sess.Windows {
		active := " "
		if w.ID == ctx.Sess.ActiveWindowID {
			active = "*"
		}
		output.WriteString(fmt.Sprintf("%-6s %-20s %-8d\n",
			fmt.Sprintf("%s%d:", active, i+1), w.Name, w.PaneCount()))
	}
	ctx.Sess.mu.Unlock()
	ctx.reply(output.String())
}

func cmdSelectWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: select-window <index|name>")
		return
	}
	ref := ctx.Args[0]

	ctx.Sess.mu.Lock()
	w := ctx.Sess.ResolveWindow(ref)
	if w != nil {
		ctx.Sess.ActiveWindowID = w.ID
	}
	ctx.Sess.mu.Unlock()

	if w == nil {
		ctx.replyErr(fmt.Sprintf("window %q not found", ref))
		return
	}
	ctx.Sess.broadcastLayout()
	ctx.reply("Switched window\n")
}

func cmdNextWindow(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	ctx.Sess.NextWindow()
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
	ctx.reply("Next window\n")
}

func cmdPrevWindow(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	ctx.Sess.PrevWindow()
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
	ctx.reply("Previous window\n")
}

func cmdRenameWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: rename-window <name>")
		return
	}
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no window")
		return
	}
	w.Name = ctx.Args[0]
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
	ctx.reply(fmt.Sprintf("Renamed window to %s\n", ctx.Args[0]))
}

func cmdResizeBorder(ctx *CommandContext) {
	if len(ctx.Args) < 3 {
		ctx.replyErr("usage: resize-border <x> <y> <delta>")
		return
	}
	x, err1 := strconv.Atoi(ctx.Args[0])
	y, err2 := strconv.Atoi(ctx.Args[1])
	delta, err3 := strconv.Atoi(ctx.Args[2])
	if err1 != nil || err2 != nil || err3 != nil {
		ctx.replyErr("resize-border: invalid arguments")
		return
	}
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w != nil {
		w.ResizeBorder(x, y, delta)
	}
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
}

func cmdResizeActive(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: resize-active <direction> <delta>")
		return
	}
	direction := ctx.Args[0]
	delta, err := strconv.Atoi(ctx.Args[1])
	if err != nil {
		ctx.replyErr("resize-active: invalid delta")
		return
	}
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w != nil {
		w.ResizeActive(direction, delta)
	}
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult})
}

func cmdResizeWindow(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: resize-window <cols> <rows>")
		return
	}
	cols, err1 := strconv.Atoi(ctx.Args[0])
	rows, err2 := strconv.Atoi(ctx.Args[1])
	if err1 != nil || err2 != nil || cols <= 0 || rows <= 0 {
		ctx.replyErr("resize-window: invalid dimensions")
		return
	}
	ctx.Sess.mu.Lock()
	layoutH := rows - render.GlobalBarHeight
	for _, w := range ctx.Sess.Windows {
		w.Resize(cols, layoutH)
	}
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcastLayout()
	ctx.reply(fmt.Sprintf("Resized to %dx%d\n", cols, rows))
}

func cmdSwap(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		return
	}

	var err error
	switch {
	case len(ctx.Args) == 1 && ctx.Args[0] == "forward":
		err = w.SwapPaneForward()
	case len(ctx.Args) == 1 && ctx.Args[0] == "backward":
		err = w.SwapPaneBackward()
	case len(ctx.Args) == 2:
		pane1 := w.ResolvePane(ctx.Args[0])
		if pane1 == nil {
			ctx.Sess.mu.Unlock()
			ctx.replyErr(fmt.Sprintf("pane %q not found", ctx.Args[0]))
			return
		}
		pane2 := w.ResolvePane(ctx.Args[1])
		if pane2 == nil {
			ctx.Sess.mu.Unlock()
			ctx.replyErr(fmt.Sprintf("pane %q not found", ctx.Args[1]))
			return
		}
		err = w.SwapPanes(pane1.ID, pane2.ID)
	default:
		ctx.Sess.mu.Unlock()
		ctx.replyErr("usage: swap <pane1> <pane2> | swap forward | swap backward")
		return
	}
	ctx.Sess.mu.Unlock()

	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcastLayout()
	ctx.reply("Swapped\n")
}

func cmdRotate(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		return
	}

	forward := true
	for _, arg := range ctx.Args {
		if arg == "--reverse" {
			forward = false
		}
	}

	w.RotatePanes(forward)
	ctx.Sess.mu.Unlock()

	ctx.Sess.broadcastLayout()
	ctx.reply("Rotated\n")
}

func cmdCopyMode(ctx *CommandContext) {
	ctx.Sess.mu.Lock()
	pane := ctx.CC.resolvePane(ctx.Sess, "copy-mode", ctx.Args)
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	paneID := pane.ID
	ctx.Sess.mu.Unlock()
	ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: paneID})
	ctx.reply(fmt.Sprintf("Copy mode entered for %s\n", pane.Meta.Name))
}

func cmdGeneration(ctx *CommandContext) {
	gen := ctx.Sess.generation.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdWaitLayout(ctx *CommandContext) {
	afterGen, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	gen, ok := ctx.Sess.waitGeneration(afterGen, timeout)
	if !ok {
		ctx.replyErr(fmt.Sprintf("timeout waiting for generation > %d (current: %d)", afterGen, gen))
		return
	}
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdClipboardGen(ctx *CommandContext) {
	gen := ctx.Sess.clipboardGen.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdWaitClipboard(ctx *CommandContext) {
	afterGen, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	data, ok := ctx.Sess.waitClipboard(afterGen, timeout)
	if !ok {
		ctx.replyErr("timeout waiting for clipboard event")
		return
	}
	ctx.reply(data + "\n")
}

func cmdWaitFor(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: wait-for <pane> <substring> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	substr := ctx.Args[1]
	timeout, err := parseTimeout(ctx.Args, 2, 10*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.Sess.mu.Lock()
	pane := ctx.CC.resolvePaneAcrossWindows(ctx.Sess, "wait-for", paneRef)
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	paneID := pane.ID
	ctx.Sess.mu.Unlock()

	if ctx.Sess.paneScreenContains(paneID, substr) {
		ctx.reply("matched\n")
		return
	}

	ch := ctx.Sess.subscribePaneOutput(paneID)
	defer ctx.Sess.unsubscribePaneOutput(paneID, ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ch:
			if ctx.Sess.paneScreenContains(paneID, substr) {
				ctx.reply("matched\n")
				return
			}
		case <-timer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %q in %s", substr, paneRef))
			return
		}
	}
}

func cmdWaitIdle(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: wait-idle <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.Sess.mu.Lock()
	pane := ctx.CC.resolvePaneAcrossWindows(ctx.Sess, "wait-idle", paneRef)
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	paneID := pane.ID
	paneName := pane.Meta.Name
	ctx.Sess.mu.Unlock()

	sub := ctx.Sess.events.Subscribe(eventFilter{Types: []string{EventIdle}, PaneName: paneName})
	defer ctx.Sess.events.Unsubscribe(sub)

	if ctx.Sess.idle.IsIdle(paneID) {
		ctx.reply("idle\n")
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-sub.ch:
		ctx.reply("idle\n")
	case <-timer.C:
		ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become idle", paneRef))
	}
}

func cmdWaitBusy(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: wait-busy <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.Sess.mu.Lock()
	pane := ctx.CC.resolvePaneAcrossWindows(ctx.Sess, "wait-busy", paneRef)
	if pane == nil {
		ctx.Sess.mu.Unlock()
		return
	}
	paneID := pane.ID
	paneName := pane.Meta.Name
	ctx.Sess.mu.Unlock()

	sub := ctx.Sess.events.Subscribe(eventFilter{Types: []string{EventBusy}, PaneName: paneName})
	defer ctx.Sess.events.Unsubscribe(sub)

	if !ctx.Sess.idle.IsIdle(paneID) {
		ctx.reply("busy\n")
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-sub.ch:
		ctx.reply("busy\n")
	case <-timer.C:
		ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become busy", paneRef))
	}
}

func cmdSetHook(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: set-hook <event> <command>")
		return
	}
	event, err := hooks.ParseEvent(ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	command := strings.Join(ctx.Args[1:], " ")
	ctx.Sess.Hooks.Add(event, command)
	ctx.reply(fmt.Sprintf("Hook added: %s → %s\n", event, command))
}

func cmdUnsetHook(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: unset-hook <event> [index]")
		return
	}
	event, err := hooks.ParseEvent(ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(ctx.Args) >= 2 {
		idx, err := strconv.Atoi(ctx.Args[1])
		if err != nil {
			ctx.replyErr(fmt.Sprintf("invalid index: %s", ctx.Args[1]))
			return
		}
		ctx.Sess.Hooks.Remove(event, idx)
		ctx.reply(fmt.Sprintf("Removed hook %d for %s\n", idx, event))
	} else {
		ctx.Sess.Hooks.RemoveAll(event)
		ctx.reply(fmt.Sprintf("Removed all hooks for %s\n", event))
	}
}

func cmdListHooks(ctx *CommandContext) {
	var output strings.Builder
	hasAny := false
	for _, event := range hooks.AllEvents {
		entries := ctx.Sess.Hooks.List(event)
		if len(entries) == 0 {
			continue
		}
		hasAny = true
		output.WriteString(fmt.Sprintf("%s:\n", event))
		for i, entry := range entries {
			output.WriteString(fmt.Sprintf("  %d: %s\n", i, entry.Command))
		}
	}
	if !hasAny {
		ctx.reply("No hooks registered.\n")
		return
	}
	ctx.reply(output.String())
}

func cmdEvents(ctx *CommandContext) {
	f := parseEventsArgs(ctx.Args)
	sub := ctx.Sess.events.Subscribe(f)
	defer ctx.Sess.events.Unsubscribe(sub)

	for _, ev := range ctx.Sess.currentStateEvents() {
		if !f.matches(ev) {
			continue
		}
		data, _ := json.Marshal(ev)
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
			return
		}
	}

	for data := range sub.ch {
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
			return
		}
	}
}

func cmdHosts(ctx *CommandContext) {
	if ctx.Sess.RemoteManager == nil {
		ctx.reply("No remote hosts configured.\n")
		return
	}
	statuses := ctx.Sess.RemoteManager.AllHostStatus()
	if len(statuses) == 0 {
		ctx.reply("No remote hosts configured.\n")
		return
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-20s %-15s\n", "HOST", "STATUS"))
	for name, state := range statuses {
		output.WriteString(fmt.Sprintf("%-20s %-15s\n", name, state))
	}
	ctx.reply(output.String())
}

func cmdDisconnect(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: disconnect <host>")
		return
	}
	if ctx.Sess.RemoteManager == nil {
		ctx.replyErr("no remote hosts configured")
		return
	}
	if err := ctx.Sess.RemoteManager.DisconnectHost(ctx.Args[0]); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Disconnected from %s\n", ctx.Args[0]))
}

func cmdReconnect(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: reconnect <host>")
		return
	}
	if ctx.Sess.RemoteManager == nil {
		ctx.replyErr("no remote hosts configured")
		return
	}
	if err := ctx.Sess.RemoteManager.ReconnectHost(ctx.Args[0], ctx.Sess.Name); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Reconnected to %s\n", ctx.Args[0]))
}

func cmdReloadServer(ctx *CommandContext) {
	execPath, err := os.Executable()
	if err != nil {
		ctx.replyErr(fmt.Sprintf("reload: %v", err))
		return
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		ctx.replyErr(fmt.Sprintf("reload: %v", err))
		return
	}
	ctx.reply("Server reloading...\n")
	if err := ctx.Srv.Reload(execPath); err != nil {
		ctx.replyErr(err.Error())
	}
}

func cmdUnsplice(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: unsplice <host>")
		return
	}
	hostName := ctx.Args[0]

	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no active window")
		return
	}

	var proxyIDs []uint32
	for _, p := range ctx.Sess.Panes {
		if p.Meta.Host == hostName && p.IsProxy() {
			proxyIDs = append(proxyIDs, p.ID)
		}
	}
	if len(proxyIDs) == 0 {
		ctx.Sess.mu.Unlock()
		ctx.replyErr(fmt.Sprintf("no spliced panes for host %q", hostName))
		return
	}

	var cellW, cellH int
	for _, p := range ctx.Sess.Panes {
		if p.Meta.Host == hostName && p.IsProxy() {
			if c := w.Root.FindPane(p.ID); c != nil && c.Parent != nil {
				cellW, cellH = c.Parent.W, c.Parent.H
				break
			}
		}
	}
	if cellW == 0 {
		cellW, cellH = w.Width, w.Height
	}
	pane, err := ctx.Sess.createPane(ctx.Srv, cellW, mux.PaneContentHeight(cellH))
	if err != nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr(err.Error())
		return
	}

	err = w.UnsplicePane(hostName, pane)
	if err != nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr(err.Error())
		return
	}

	for _, id := range proxyIDs {
		ctx.Sess.removePane(id)
	}
	ctx.Sess.mu.Unlock()

	pane.Start()
	ctx.Sess.broadcastLayout()
	ctx.reply(fmt.Sprintf("Unspliced %s: %d proxy panes removed\n", hostName, len(proxyIDs)))
}

func cmdInjectProxy(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: _inject-proxy <host>")
		return
	}
	hostName := ctx.Args[0]
	ctx.Sess.mu.Lock()
	w := ctx.Sess.ActiveWindow()
	if w == nil {
		ctx.Sess.mu.Unlock()
		ctx.replyErr("no window")
		return
	}
	id := ctx.Sess.counter.Add(1)
	meta := mux.PaneMeta{
		Name:  fmt.Sprintf(mux.PaneNameFormat, id),
		Host:  hostName,
		Color: config.CatppuccinMocha[0], // Rosewater
	}
	proxyPane := mux.NewProxyPane(id, meta, w.Width/2, mux.PaneContentHeight(w.Height),
		ctx.Sess.paneOutputCallback(),
		ctx.Sess.paneExitCallback(ctx.Srv),
		func(data []byte) (int, error) { return len(data), nil },
	)
	ctx.Sess.Panes = append(ctx.Sess.Panes, proxyPane)
	_, err := w.Split(mux.SplitHorizontal, proxyPane)
	ctx.Sess.mu.Unlock()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcastLayout()
	ctx.reply(fmt.Sprintf("Injected proxy pane %s @%s\n", meta.Name, hostName))
}
