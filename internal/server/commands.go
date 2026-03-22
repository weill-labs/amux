package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/hooks"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	"github.com/weill-labs/amux/internal/render"
)

// CommandHandler processes a single CLI command.
type CommandHandler func(ctx *CommandContext)

// tokenKeyGap is a small pacing gap before injected submit/control keys.
// Some interactive TUIs only react correctly when Enter or Ctrl-key input
// arrives on a later input tick rather than in the same burst as preceding text.
const tokenKeyGap = 50 * time.Millisecond

const broadcastUsage = "usage: broadcast (--panes <pane,pane,...> | --window <index|name> | --match <glob>) [--hex] <keys>..."

// ReloadServerExecPathFlag carries the requesting CLI's resolved executable
// path so the server can re-exec that binary instead of its original launch
// path when the two differ.
const ReloadServerExecPathFlag = "--exec-path"

var resolveServerReloadExecPath = reload.ResolveExecutable

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

func (ctx *CommandContext) replyCommandMutation(res commandMutationResult) {
	if res.err != nil {
		ctx.replyErr(res.err.Error())
		return
	}
	for _, pane := range res.startPanes {
		pane.Start()
	}
	for _, pane := range res.closePanes {
		pane.Close()
	}
	if res.output != "" {
		ctx.reply(res.output)
	} else {
		ctx.CC.Send(&Message{Type: MsgTypeCmdResult})
	}
	if res.sendExit {
		ctx.Sess.broadcast(&Message{Type: MsgTypeExit})
	}
	if res.shutdownServer {
		go ctx.Srv.Shutdown()
	}
}

func (ctx *CommandContext) activeWindowSnapshot() (activePid, width, height int, err error) {
	snap, err := ctx.Sess.queryActiveWindowSnapshot()
	if err != nil {
		return 0, 0, 0, err
	}
	return snap.activePID, snap.width, snap.height, nil
}

type splitCommandArgs struct {
	rootLevel bool
	dir       mux.SplitDir
	hostName  string
	name      string
}

func parseSplitCommandArgs(args []string) (splitCommandArgs, error) {
	parsed := splitCommandArgs{dir: mux.SplitHorizontal}
	hasExplicitDir := false

	setDir := func(next mux.SplitDir) error {
		if hasExplicitDir && parsed.dir != next {
			return fmt.Errorf("conflicting split directions")
		}
		parsed.dir = next
		hasExplicitDir = true
		return nil
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "root":
			parsed.rootLevel = true
		case "v", "--vertical":
			if err := setDir(mux.SplitVertical); err != nil {
				return splitCommandArgs{}, err
			}
		case "--horizontal":
			if err := setDir(mux.SplitHorizontal); err != nil {
				return splitCommandArgs{}, err
			}
		case "--host":
			if i+1 >= len(args) {
				return splitCommandArgs{}, fmt.Errorf("--host requires a value")
			}
			parsed.hostName = args[i+1]
			i++
		case "--name":
			if i+1 >= len(args) {
				return splitCommandArgs{}, fmt.Errorf("--name requires a value")
			}
			parsed.name = args[i+1]
			i++
		default:
			return splitCommandArgs{}, fmt.Errorf("unknown split arg %q", args[i])
		}
	}

	return parsed, nil
}

func activePaneRender(w *mux.Window) []paneRender {
	if w == nil || w.ActivePane == nil {
		return nil
	}
	return []paneRender{{
		paneID: w.ActivePane.ID,
		data:   []byte(w.ActivePane.RenderScreen()),
	}}
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
	"broadcast":       cmdBroadcast,
	"status":          cmdStatus,
	"new-window":      cmdNewWindow,
	"list-windows":    cmdListWindows,
	"list-clients":    cmdListClients,
	"select-window":   cmdSelectWindow,
	"next-window":     cmdNextWindow,
	"prev-window":     cmdPrevWindow,
	"rename-window":   cmdRenameWindow,
	"resize-border":   cmdResizeBorder,
	"resize-active":   cmdResizeActive,
	"resize-pane":     cmdResizePane,
	"resize-window":   cmdResizeWindow,
	"swap":            cmdSwap,
	"rotate":          cmdRotate,
	"copy-mode":       cmdCopyMode,
	"generation":      cmdGeneration,
	"wait-layout":     cmdWaitLayout,
	"clipboard-gen":   cmdClipboardGen,
	"wait-clipboard":  cmdWaitClipboard,
	"hook-gen":        cmdHookGen,
	"wait-hook":       cmdWaitHook,
	"wait-for":        cmdWaitFor,
	"wait-idle":       cmdWaitIdle,
	"wait-busy":       cmdWaitBusy,
	"ui-gen":          cmdUIGen,
	"wait-ui":         cmdWaitUI,
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
	"type-keys":       cmdTypeKeys,
	"set-meta":        cmdSetMeta,
	"add-meta":        cmdAddMeta,
	"rm-meta":         cmdRmMeta,
}

func cmdList(ctx *CommandContext) {
	entries, err := ctx.Sess.queryPaneList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	var output string
	if len(entries) == 0 {
		output = "No panes.\n"
	} else {
		output = fmt.Sprintf("%-6s %-20s %-15s %-30s %-10s %-12s %s\n", "PANE", "NAME", "HOST", "BRANCH", "WINDOW", "TASK", "META")
		for _, p := range entries {
			active := " "
			if p.active {
				active = "*"
			}
			branch := p.gitBranch
			if p.pr != "" {
				branch += " #" + p.pr
			}
			output += fmt.Sprintf("%-6s %-20s %-15s %-30s %-10s %-12s %s\n",
				fmt.Sprintf("%s%d", active, p.paneID),
				p.name, p.host, branch, p.windowName, p.task, formatMetaCollections(p.prs, p.issues))
		}
	}
	ctx.reply(output)
}

func cmdSplit(ctx *CommandContext) {
	args, err := parseSplitCommandArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	// If no --host flag, inherit the active pane's host when it's a
	// remote proxy pane. Splitting a remote pane should stay remote.
	if args.hostName == "" {
		snap, err := ctx.Sess.queryActiveWindowSnapshot()
		if err == nil {
			args.hostName = snap.proxyHost
		}
	}

	if args.hostName != "" {
		pane, err := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, args.hostName, args.dir, args.rootLevel, args.name)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.reply(fmt.Sprintf("Split %s: new remote pane %s @%s\n", dirName(args.dir), pane.Meta.Name, args.hostName))
	} else {
		activePid, _, _, err := ctx.activeWindowSnapshot()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		meta := mux.PaneMeta{Name: args.name, Dir: mux.PaneCwd(activePid)}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			w := sess.ActiveWindow()
			if w == nil {
				return commandMutationResult{err: fmt.Errorf("no window")}
			}
			pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
			if err != nil {
				return commandMutationResult{err: err}
			}
			if args.rootLevel {
				_, err = w.SplitRoot(args.dir, pane)
			} else {
				_, err = w.Split(args.dir, pane)
			}
			if err != nil {
				sess.removePane(pane.ID)
				pane.Close()
				return commandMutationResult{err: err}
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Split %s: new pane %s\n", dirName(args.dir), pane.Meta.Name),
				broadcastLayout: true,
				startPanes:      []*mux.Pane{pane},
			}
		}))
	}
}

func cmdFocus(ctx *CommandContext) {
	direction := "next"
	if len(ctx.Args) > 0 {
		direction = ctx.Args[0]
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		switch direction {
		case "next", "left", "right", "up", "down":
			w.Focus(direction)
			pane := w.ActivePane
			return commandMutationResult{
				output:          fmt.Sprintf("Focused %s\n", pane.Meta.Name),
				broadcastLayout: true,
				paneRenders:     activePaneRender(w),
			}
		default:
			pane, pw, err := sess.resolvePaneAcrossWindows(direction)
			if err != nil {
				return commandMutationResult{err: err}
			}
			if pw != nil {
				sess.ActiveWindowID = pw.ID
				pw.FocusPane(pane)
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Focused %s\n", pane.Meta.Name),
				broadcastLayout: true,
				paneRenders:     activePaneRender(pw),
			}
		}
	}))
}

func cmdCapture(ctx *CommandContext) {
	if caputil.ParseArgs(ctx.Args).HistoryMode {
		ctx.CC.Send(ctx.Sess.captureHistory(ctx.CC, ctx.Args))
		return
	}
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
		pane, err := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, remoteHost, mux.SplitVertical, false, meta.Name)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			registered := sess.findPaneByID(pane.ID)
			if registered == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", pane.Meta.Name)}
			}
			registered.Meta.Name = meta.Name
			if meta.Task != "" {
				registered.Meta.Task = meta.Task
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Spawned %s in pane %d @%s\n", meta.Name, pane.ID, remoteHost),
				broadcastLayout: true,
			}
		}))
	} else {
		activePid, _, _, err := ctx.activeWindowSnapshot()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if meta.Dir == "" {
			meta.Dir = mux.PaneCwd(activePid)
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			w := sess.ActiveWindow()
			if w == nil {
				return commandMutationResult{err: fmt.Errorf("no window")}
			}
			pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
			if err != nil {
				return commandMutationResult{err: err}
			}
			if _, err := w.Split(mux.SplitVertical, pane); err != nil {
				sess.removePane(pane.ID)
				pane.Close()
				return commandMutationResult{err: err}
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Spawned %s in pane %d\n", meta.Name, pane.ID),
				broadcastLayout: true,
				startPanes:      []*mux.Pane{pane},
			}
		}))
	}
}

func cmdZoom(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		var pane *mux.Pane
		if len(ctx.Args) > 0 {
			pane = w.ResolvePane(ctx.Args[0])
			if pane == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", ctx.Args[0])}
			}
		} else {
			pane = w.ActivePane
		}
		if pane == nil {
			return commandMutationResult{err: fmt.Errorf("no active pane")}
		}
		willUnzoom := w.ZoomedPaneID == pane.ID
		if err := w.Zoom(pane.ID); err != nil {
			return commandMutationResult{err: err}
		}
		verb := "Zoomed"
		if willUnzoom {
			verb = "Unzoomed"
		}
		return commandMutationResult{
			output:          fmt.Sprintf("%s %s\n", verb, pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdMinimize(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		p, w, err := sess.resolvePaneWindow("minimize", ctx.Args)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.Minimize(p.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Minimized %s\n", p.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdRestore(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		p, w, err := sess.resolvePaneWindow("restore", ctx.Args)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.Restore(p.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Restored %s\n", p.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdToggleMinimize(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no active window")}
		}
		name, didMinimize, err := w.ToggleMinimize()
		if err != nil {
			return commandMutationResult{err: err}
		}
		verb := "Restored"
		if didMinimize {
			verb = "Minimized"
		}
		return commandMutationResult{
			output:          fmt.Sprintf("%s %s\n", verb, name),
			broadcastLayout: true,
		}
	}))
}

func cmdKill(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		var pane *mux.Pane
		if len(ctx.Args) == 0 {
			w := sess.ActiveWindow()
			if w != nil {
				pane = w.ActivePane
			}
		} else {
			var err error
			pane, _, err = sess.resolvePaneAcrossWindows(ctx.Args[0])
			if err != nil {
				return commandMutationResult{err: err}
			}
		}
		if pane == nil {
			return commandMutationResult{}
		}

		paneID := pane.ID
		paneName := pane.Meta.Name
		lastPane := len(sess.Panes) <= 1
		sess.removePane(paneID)
		closedWindow := sess.closePaneInWindow(paneID)

		res := commandMutationResult{
			closePanes: []*mux.Pane{pane},
		}
		if lastPane {
			res.output = fmt.Sprintf("Killed %s (session exiting)\n", paneName)
			res.sendExit = true
			res.shutdownServer = true
			return res
		}

		res.broadcastLayout = true
		if closedWindow != "" {
			res.output = fmt.Sprintf("Killed %s (closed %s)\n", paneName, closedWindow)
		} else {
			res.output = fmt.Sprintf("Killed %s\n", paneName)
		}
		return res
	}))
}

func cmdSendKeys(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: send-keys <pane> [--hex] <keys>...")
		return
	}
	hexMode, keys := parseKeyArgs(ctx.Args[1:])
	chunks, err := encodeKeyChunks(hexMode, keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	pane, err := ctx.Sess.queryResolvedPane(ctx.Args[0])
	if err != nil {
		if len(ctx.Args) < 1 {
			ctx.replyErr("usage: send-keys <pane> [--hex] <keys>...")
		} else {
			ctx.replyErr(err.Error())
		}
		return
	}
	if err := ctx.Sess.enqueuePacedPaneInput(pane.pane, chunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Sent %d bytes to %s\n", totalEncodedKeyBytes(chunks), pane.paneName))
}

type broadcastCommandArgs struct {
	paneRefs     []string
	windowRef    string
	matchPattern string
	hexMode      bool
	keys         []string
}

func cmdBroadcast(ctx *CommandContext) {
	parsed, err := parseBroadcastCommandArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	chunks, err := encodeKeyChunks(parsed.hexMode, parsed.keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	targets, err := resolveBroadcastTargets(ctx.Sess, parsed)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	if err := enqueueBroadcastInput(ctx.Sess, targets, chunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}

	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.paneName)
	}

	noun := "panes"
	if len(targets) == 1 {
		noun = "pane"
	}
	ctx.reply(fmt.Sprintf("Sent %d bytes to %d %s: %s\n", totalEncodedKeyBytes(chunks), len(targets), noun, strings.Join(names, ", ")))
}

func parseBroadcastCommandArgs(args []string) (broadcastCommandArgs, error) {
	if len(args) == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}

	var parsed broadcastCommandArgs
	var keyArgs []string
	selectorCount := 0

	for i := 0; i < len(args); {
		switch args[i] {
		case "--":
			i++
			keyArgs = append(keyArgs, args[i:]...)
			i = len(args)
		case "--panes":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.paneRefs = splitBroadcastPaneRefs(args[i+1])
			if len(parsed.paneRefs) == 0 {
				return broadcastCommandArgs{}, fmt.Errorf("broadcast: no panes specified")
			}
			i += 2
		case "--window":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.windowRef = args[i+1]
			i += 2
		case "--match":
			if i+1 >= len(args) {
				return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
			}
			selectorCount++
			parsed.matchPattern = args[i+1]
			i += 2
		default:
			keyArgs = append(keyArgs, args[i:]...)
			i = len(args)
		}
	}

	if selectorCount == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}
	if selectorCount != 1 {
		return broadcastCommandArgs{}, fmt.Errorf("broadcast: specify exactly one of --panes, --window, or --match")
	}

	parsed.hexMode, parsed.keys = parseKeyArgs(keyArgs)
	if len(parsed.keys) == 0 {
		return broadcastCommandArgs{}, fmt.Errorf(broadcastUsage)
	}

	return parsed, nil
}

func splitBroadcastPaneRefs(raw string) []string {
	var refs []string
	for _, ref := range strings.Split(raw, ",") {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	return refs
}

func resolveBroadcastTargets(sess *Session, args broadcastCommandArgs) ([]resolvedPaneRef, error) {
	return enqueueSessionQuery(sess, func(sess *Session) ([]resolvedPaneRef, error) {
		switch {
		case len(args.paneRefs) > 0:
			return resolveBroadcastPaneRefs(sess, args.paneRefs)
		case args.windowRef != "":
			return resolveBroadcastWindowTargets(sess, args.windowRef)
		case args.matchPattern != "":
			return resolveBroadcastMatchTargets(sess, args.matchPattern)
		default:
			return nil, fmt.Errorf(broadcastUsage)
		}
	})
}

func resolveBroadcastPaneRefs(sess *Session, refs []string) ([]resolvedPaneRef, error) {
	targets := make([]resolvedPaneRef, 0, len(refs))
	seen := make(map[uint32]struct{}, len(refs))
	for _, ref := range refs {
		pane, window, err := sess.resolvePaneAcrossWindows(ref)
		if err != nil {
			return nil, err
		}
		targets = appendBroadcastTarget(targets, seen, pane, window)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: no panes specified")
	}
	return targets, nil
}

func resolveBroadcastWindowTargets(sess *Session, ref string) ([]resolvedPaneRef, error) {
	window := sess.ResolveWindow(ref)
	if window == nil {
		return nil, fmt.Errorf("window %q not found", ref)
	}

	targets := make([]resolvedPaneRef, 0, window.PaneCount())
	seen := make(map[uint32]struct{}, window.PaneCount())
	window.Root.Walk(func(cell *mux.LayoutCell) {
		if cell.Pane == nil {
			return
		}
		targets = appendBroadcastTarget(targets, seen, cell.Pane, window)
	})
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: window %q has no panes", ref)
	}
	return targets, nil
}

func resolveBroadcastMatchTargets(sess *Session, pattern string) ([]resolvedPaneRef, error) {
	targets := make([]resolvedPaneRef, 0, len(sess.Panes))
	seen := make(map[uint32]struct{}, len(sess.Panes))
	for _, pane := range sess.Panes {
		matched, err := filepath.Match(pattern, pane.Meta.Name)
		if err != nil {
			return nil, fmt.Errorf("broadcast: invalid match pattern %q: %v", pattern, err)
		}
		if !matched {
			continue
		}
		targets = appendBroadcastTarget(targets, seen, pane, sess.FindWindowByPaneID(pane.ID))
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("broadcast: no panes match %q", pattern)
	}
	return targets, nil
}

func appendBroadcastTarget(targets []resolvedPaneRef, seen map[uint32]struct{}, pane *mux.Pane, window *mux.Window) []resolvedPaneRef {
	if pane == nil {
		return targets
	}
	if _, ok := seen[pane.ID]; ok {
		return targets
	}
	seen[pane.ID] = struct{}{}

	target := resolvedPaneRef{
		pane:     pane,
		window:   window,
		paneID:   pane.ID,
		paneName: pane.Meta.Name,
	}
	if window != nil {
		target.windowID = window.ID
	}
	return append(targets, target)
}

func enqueueBroadcastInput(sess *Session, targets []resolvedPaneRef, chunks []encodedKeyChunk) error {
	var wg sync.WaitGroup
	errs := make(chan string, len(targets))

	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sess.enqueuePacedPaneInput(target.pane, chunks); err != nil {
				errs <- fmt.Sprintf("%s: %v", target.paneName, err)
			}
		}()
	}

	wg.Wait()
	close(errs)

	if len(errs) == 0 {
		return nil
	}

	failures := make([]string, 0, len(errs))
	for err := range errs {
		failures = append(failures, err)
	}
	slices.Sort(failures)
	return fmt.Errorf("broadcast: failed for %d/%d panes: %s", len(failures), len(targets), strings.Join(failures, ", "))
}

func totalEncodedKeyBytes(chunks []encodedKeyChunk) int {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk.data)
	}
	return total
}

func cmdStatus(ctx *CommandContext) {
	snap, err := ctx.Sess.querySessionStatus()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	active := snap.total - snap.minimized
	statusLine := fmt.Sprintf("windows: %d, panes: %d total, %d active, %d minimized", snap.windowCount, snap.total, active, snap.minimized)
	if snap.zoomed != "" {
		statusLine += fmt.Sprintf(", %s zoomed", snap.zoomed)
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
	activePid, _, _, err := ctx.activeWindowSnapshot()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	meta := mux.PaneMeta{Dir: mux.PaneCwd(activePid)}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}

		winID := sess.windowCounter.Add(1)
		newWin := mux.NewWindow(pane, w.Width, w.Height)
		newWin.ID = winID
		if name != "" {
			newWin.Name = name
		} else {
			newWin.Name = fmt.Sprintf(WindowNameFormat, winID)
		}
		sess.Windows = append(sess.Windows, newWin)
		sess.ActiveWindowID = winID

		return commandMutationResult{
			output:          fmt.Sprintf("Created %s\n", newWin.Name),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdListWindows(ctx *CommandContext) {
	entries, err := ctx.Sess.queryWindowList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-6s %-20s %-8s\n", "WIN", "NAME", "PANES"))
	for _, w := range entries {
		active := " "
		if w.active {
			active = "*"
		}
		output.WriteString(fmt.Sprintf("%-6s %-20s %-8d\n",
			fmt.Sprintf("%s%d:", active, w.index), w.name, w.paneCount))
	}
	ctx.reply(output.String())
}

func cmdSelectWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: select-window <index|name>")
		return
	}
	ref := ctx.Args[0]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ResolveWindow(ref)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("window %q not found", ref)}
		}
		sess.ActiveWindowID = w.ID
		return commandMutationResult{
			output:          "Switched window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(w),
		}
	}))
}

func cmdNextWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.NextWindow()
		return commandMutationResult{
			output:          "Next window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.ActiveWindow()),
		}
	}))
}

func cmdPrevWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.PrevWindow()
		return commandMutationResult{
			output:          "Previous window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.ActiveWindow()),
		}
	}))
}

func cmdRenameWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: rename-window <name>")
		return
	}
	name := ctx.Args[0]
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		w.Name = name
		return commandMutationResult{
			output:          fmt.Sprintf("Renamed window to %s\n", name),
			broadcastLayout: true,
		}
	}))
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
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if w := sess.ActiveWindow(); w != nil {
			w.ResizeBorder(x, y, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
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
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if w := sess.ActiveWindow(); w != nil {
			w.ResizeActive(direction, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdResizePane(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: resize-pane <pane> <direction> [delta]")
		return
	}
	direction := ctx.Args[1]
	switch direction {
	case "left", "right", "up", "down":
	default:
		ctx.replyErr(fmt.Sprintf("resize-pane: invalid direction %q (use left/right/up/down)", direction))
		return
	}
	delta := 1
	if len(ctx.Args) >= 3 {
		var err error
		delta, err = strconv.Atoi(ctx.Args[2])
		if err != nil || delta <= 0 {
			ctx.replyErr("resize-pane: invalid delta")
			return
		}
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		p, w, err := sess.resolvePaneWindow("resize-pane", ctx.Args[:1])
		if err != nil {
			return commandMutationResult{err: err}
		}
		w.ResizePane(p.ID, direction, delta)
		return commandMutationResult{
			output:          fmt.Sprintf("Resized %s %s by %d\n", p.Meta.Name, direction, delta),
			broadcastLayout: true,
		}
	}))
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
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		layoutH := rows - render.GlobalBarHeight
		for _, w := range sess.Windows {
			w.Resize(cols, layoutH)
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Resized to %dx%d\n", cols, rows),
			broadcastLayout: true,
		}
	}))
}

func cmdSwap(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
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
				return commandMutationResult{err: fmt.Errorf("pane %q not found", ctx.Args[0])}
			}
			pane2 := w.ResolvePane(ctx.Args[1])
			if pane2 == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", ctx.Args[1])}
			}
			err = w.SwapPanes(pane1.ID, pane2.ID)
		default:
			return commandMutationResult{err: fmt.Errorf("usage: swap <pane1> <pane2> | swap forward | swap backward")}
		}
		if err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Swapped\n", broadcastLayout: true}
	}))
}

func cmdRotate(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		forward := true
		for _, arg := range ctx.Args {
			if arg == "--reverse" {
				forward = false
			}
		}

		w.RotatePanes(forward)
		return commandMutationResult{output: "Rotated\n", broadcastLayout: true}
	}))
}

func cmdCopyMode(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (resolvedPaneRef, error) {
			w := sess.ActiveWindow()
			if w == nil || w.ActivePane == nil {
				return resolvedPaneRef{}, fmt.Errorf("no active pane")
			}
			return resolvedPaneRef{
				pane:     w.ActivePane,
				window:   w,
				paneID:   w.ActivePane.ID,
				paneName: w.ActivePane.Meta.Name,
				windowID: w.ID,
			}, nil
		})
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: pane.paneID})
		ctx.reply(fmt.Sprintf("Copy mode entered for %s\n", pane.paneName))
		return
	}
	pane, err := ctx.Sess.queryResolvedPane(ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: pane.paneID})
	ctx.reply(fmt.Sprintf("Copy mode entered for %s\n", pane.paneName))
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

func cmdHookGen(ctx *CommandContext) {
	gen := ctx.Sess.hookGen.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func parseUIGenArgs(args []string) (clientID string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--client":
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for --client")
			}
			i++
			clientID = args[i]
		default:
			return "", fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return clientID, nil
}

func cmdUIGen(ctx *CommandContext) {
	requestedClientID, err := parseUIGenArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	client, err := ctx.Sess.queryUIClient(requestedClientID, "")
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("%d\n", client.currentGen))
}

func parseWaitHookArgs(args []string) (eventName, paneName string, afterGen uint64, timeout time.Duration, err error) {
	if len(args) < 1 {
		return "", "", 0, 0, fmt.Errorf("usage: wait-hook <event> [--pane <pane>] [--after N] [--timeout <duration>]")
	}
	eventName = args[0]
	timeout = 5 * time.Second
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--pane":
			if i+1 >= len(args) {
				return "", "", 0, 0, fmt.Errorf("missing value for --pane")
			}
			i++
			paneName = args[i]
		case "--after":
			if i+1 >= len(args) {
				return "", "", 0, 0, fmt.Errorf("missing value for --after")
			}
			i++
			afterGen, err = strconv.ParseUint(args[i], 10, 64)
			if err != nil {
				return "", "", 0, 0, fmt.Errorf("invalid --after generation: %s", args[i])
			}
		case "--timeout":
			if i+1 >= len(args) {
				return "", "", 0, 0, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err = time.ParseDuration(args[i])
			if err != nil {
				return "", "", 0, 0, err
			}
		default:
			return "", "", 0, 0, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	if _, err := hooks.ParseEvent(eventName); err != nil {
		return "", "", 0, 0, err
	}
	return eventName, paneName, afterGen, timeout, nil
}

func resolveWaitHookPaneName(ctx *CommandContext, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	pane, err := ctx.Sess.queryResolvedPane(ref)
	if err != nil {
		return "", err
	}
	return pane.paneName, nil
}

func cmdWaitHook(ctx *CommandContext) {
	eventName, paneName, afterGen, timeout, err := parseWaitHookArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneName, err = resolveWaitHookPaneName(ctx, paneName)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	record, ok := ctx.Sess.waitHook(afterGen, eventName, paneName, timeout)
	if !ok {
		target := eventName
		if paneName != "" {
			target += " on " + paneName
		}
		ctx.replyErr(fmt.Sprintf("timeout waiting for hook %s", target))
		return
	}
	status := "success"
	if !record.Success {
		status = "failure"
	}
	ctx.reply(fmt.Sprintf("%d %s %s %s\n", record.Generation, record.Event, record.PaneName, status))
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

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	if ctx.Sess.paneScreenContains(paneID, substr) {
		ctx.reply("matched\n")
		return
	}

	ch := ctx.Sess.enqueuePaneOutputSubscribe(paneID)
	if ch == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, ch)

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

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID
	paneName := pane.paneName

	checkIdle := func() (bool, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return false, err
		}
		if pane == nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
		}
		if !pane.AgentStatus().Idle {
			return false, nil
		}
		return true, nil
	}

	res := ctx.Sess.enqueueEventSubscribe(eventFilter{Types: []string{EventIdle}, PaneName: paneName}, false)
	if res.sub == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	if ctx.Sess.idle.IsIdle(paneID) {
		idle, err := checkIdle()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if idle {
			ctx.reply("idle\n")
			return
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-res.sub.ch:
			idle, err := checkIdle()
			if err != nil {
				ctx.replyErr(err.Error())
				return
			}
			if idle {
				ctx.reply("idle\n")
				return
			}
		case <-timer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become idle", paneRef))
			return
		}
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

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	checkBusy := func() (bool, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return false, err
		}
		if pane == nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return !pane.AgentStatus().Idle, nil
	}
	checkBusyStatus := func() (mux.AgentStatus, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return mux.AgentStatus{}, err
		}
		if pane == nil {
			return mux.AgentStatus{}, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return pane.AgentStatus(), nil
	}

	outputCh := ctx.Sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	busy, err := checkBusy()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if busy {
		// Require the same foreground child across two checks so transient
		// prompt helpers do not satisfy wait-busy.
		st, err := checkBusyStatus()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		candidatePID := waitBusyForegroundPID(st)
		if candidatePID != 0 {
			time.Sleep(50 * time.Millisecond)
			st2, err := checkBusyStatus()
			if err != nil {
				ctx.replyErr(err.Error())
				return
			}
			if _, ready := waitBusyReady(candidatePID, st2); ready {
				ctx.reply("busy\n")
				return
			}
		}
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	candidatePID := 0

	for {
		select {
		case <-outputCh:
		case <-ticker.C:
		case <-timeoutTimer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become busy", paneRef))
			return
		}

		st, err := checkBusyStatus()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		nextPID, ready := waitBusyReady(candidatePID, st)
		if ready {
			ctx.reply("busy\n")
			return
		}
		candidatePID = nextPID
	}
}

func waitBusyForegroundPID(status mux.AgentStatus) int {
	if status.Idle || len(status.ChildPIDs) == 0 {
		return 0
	}
	return status.ChildPIDs[len(status.ChildPIDs)-1]
}

func waitBusyReady(candidatePID int, status mux.AgentStatus) (nextPID int, ready bool) {
	nextPID = waitBusyForegroundPID(status)
	return nextPID, nextPID != 0 && nextPID == candidatePID
}

func parseWaitUIArgs(args []string) (eventName, clientID string, afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	if len(args) < 1 {
		return "", "", 0, false, 0, fmt.Errorf("usage: wait-ui <event> [--client <id>] [--after N] [--timeout <duration>]")
	}
	eventName = args[0]
	timeout = 5 * time.Second
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--client":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --client")
			}
			i++
			clientID = args[i]
		case "--after":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --after")
			}
			i++
			afterSet = true
			afterGen, err = strconv.ParseUint(args[i], 10, 64)
			if err != nil {
				return "", "", 0, false, 0, fmt.Errorf("invalid --after generation: %s", args[i])
			}
		case "--timeout":
			if i+1 >= len(args) {
				return "", "", 0, false, 0, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err = time.ParseDuration(args[i])
			if err != nil {
				return "", "", 0, false, 0, fmt.Errorf("invalid timeout: %s", args[i])
			}
		default:
			return "", "", 0, false, 0, fmt.Errorf("unknown flag: %s", args[i])
		}
	}
	return eventName, clientID, afterGen, afterSet, timeout, nil
}

func cmdWaitUI(ctx *CommandContext) {
	eventName, requestedClientID, afterGen, afterSet, timeout, err := parseWaitUIArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if !proto.IsKnownUIEvent(eventName) {
		ctx.replyErr(errUnknownUIEvent(eventName).Error())
		return
	}

	subscription, err := ctx.Sess.enqueueUIWaitSubscribe(requestedClientID, eventName)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(subscription.sub)

	if subscription.currentMatch && (!afterSet || subscription.currentGen > afterGen) {
		ctx.reply(eventName + "\n")
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-subscription.sub.ch:
		ctx.reply(eventName + "\n")
	case <-timer.C:
		ctx.replyErr(fmt.Sprintf("timeout waiting for %s on %s", eventName, subscription.clientID))
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
	ea := parseEventsArgs(ctx.Args)
	res := ctx.Sess.enqueueEventSubscribe(ea.filter, true)
	if res.sub == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	// Send initial state computed atomically at subscribe time.
	for _, data := range res.initialState {
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
			return
		}
	}

	if ea.throttle <= 0 {
		// No throttle: pass all events through immediately.
		for data := range res.sub.ch {
			if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
				return
			}
		}
		return
	}

	// Throttle: coalesce output events, pass non-output through immediately.
	ticker := time.NewTicker(ea.throttle)
	defer ticker.Stop()
	pending := make(map[uint32][]byte) // pane ID → last output event JSON

	for {
		select {
		case data, ok := <-res.sub.ch:
			if !ok {
				return
			}
			if paneID, isOutput := peekOutputPaneID(data); isOutput {
				pending[paneID] = data
			} else {
				if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
					return
				}
			}
		case <-ticker.C:
			if err := flushPendingOutputEvents(ctx, pending); err != nil {
				return
			}
		}
	}
}

// flushPendingOutputEvents sends all pending output events in sorted pane ID
// order, then clears the map.
func flushPendingOutputEvents(ctx *CommandContext, pending map[uint32][]byte) error {
	if len(pending) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(pending[id]) + "\n"}); err != nil {
			return err
		}
		delete(pending, id)
	}
	return nil
}

// peekOutputPaneID checks if data is an output event and returns the pane ID.
// Uses bytes.Contains for the type check to avoid unmarshalling non-output events.
func peekOutputPaneID(data []byte) (uint32, bool) {
	if !bytes.Contains(data, []byte(`"type":"output"`)) {
		return 0, false
	}
	var partial struct {
		PaneID uint32 `json:"pane_id"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return 0, false
	}
	return partial.PaneID, true
}

func cmdListClients(ctx *CommandContext) {
	clients, err := ctx.Sess.queryClientList()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(clients) == 0 {
		ctx.reply("No clients attached.\n")
		return
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-10s %-8s %-15s %-10s %-10s %s\n", "CLIENT", "OWNER", "SIZE", "DISPLAY_PANES", "CHOOSER", "CAPABILITIES"))
	for _, cc := range clients {
		owner := ""
		if cc.sizeOwner {
			owner = "*"
		}
		output.WriteString(fmt.Sprintf("%-10s %-8s %-15s %-10s %-10s %s\n", cc.id, owner, cc.size, cc.displayPanes, cc.chooser, cc.capabilities))
	}
	ctx.reply(output.String())
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
	execPath, err := requestedReloadExecPath(ctx.Args)
	if err != nil {
		ctx.replyErr(fmt.Sprintf("reload: %v", err))
		return
	}
	if execPath == "" {
		execPath, err = resolveServerReloadExecPath()
		if err != nil {
			ctx.replyErr(fmt.Sprintf("reload: %v", err))
			return
		}
	}
	ctx.reply("Server reloading...\n")
	if err := ctx.Srv.Reload(execPath); err != nil {
		ctx.replyErr(err.Error())
	}
}

func requestedReloadExecPath(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != ReloadServerExecPathFlag {
			continue
		}
		if i+1 >= len(args) {
			return "", fmt.Errorf("missing value for %s", ReloadServerExecPathFlag)
		}
		return filepath.EvalSymlinks(args[i+1])
	}
	return "", nil
}

func cmdUnsplice(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: unsplice <host>")
		return
	}
	hostName := ctx.Args[0]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no active window")}
		}

		var proxyIDs []uint32
		for _, p := range sess.Panes {
			if p.Meta.Host == hostName && p.IsProxy() {
				proxyIDs = append(proxyIDs, p.ID)
			}
		}
		if len(proxyIDs) == 0 {
			return commandMutationResult{err: fmt.Errorf("no spliced panes for host %q", hostName)}
		}

		var cellW, cellH int
		for _, p := range sess.Panes {
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

		pane, err := sess.createPane(ctx.Srv, cellW, mux.PaneContentHeight(cellH))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.UnsplicePane(hostName, pane); err != nil {
			sess.removePane(pane.ID)
			pane.Close()
			return commandMutationResult{err: err}
		}

		for _, id := range proxyIDs {
			sess.removePane(id)
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Unspliced %s: %d proxy panes removed\n", hostName, len(proxyIDs)),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdTypeKeys(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		ctx.replyErr("usage: type-keys [--hex] <keys>...")
		return
	}
	hexMode, keys := parseKeyArgs(ctx.Args)
	if len(keys) == 0 {
		ctx.replyErr("usage: type-keys [--hex] <keys>...")
		return
	}
	chunks, err := encodeKeyChunks(hexMode, keys)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	client, err := ctx.Sess.queryFirstClient()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	if err := client.enqueueTypeKeys(chunks); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Typed %d bytes\n", totalEncodedKeyBytes(chunks)))
}

// parseKeyArgs splits args into a hex-mode flag and the remaining key tokens.
func parseKeyArgs(args []string) (hexMode bool, keys []string) {
	for _, arg := range args {
		if arg == "--hex" {
			hexMode = true
		} else {
			keys = append(keys, arg)
		}
	}
	return hexMode, keys
}

type encodedKeyChunk struct {
	data       []byte
	paceBefore bool
}

// encodeKeyChunks converts key tokens to raw byte chunks while preserving
// token boundaries. In hex mode, tokens are hex-decoded; otherwise each token
// is passed through parseKey.
func encodeKeyChunks(hexMode bool, keys []string) ([]encodedKeyChunk, error) {
	var chunks []encodedKeyChunk
	if hexMode {
		for _, hexStr := range keys {
			b, err := hex.DecodeString(hexStr)
			if err != nil {
				return nil, fmt.Errorf("invalid hex: %s", hexStr)
			}
			chunks = append(chunks, encodedKeyChunk{data: b})
		}
	} else {
		for _, key := range keys {
			chunks = append(chunks, encodedKeyChunk{
				data:       parseKey(key),
				paceBefore: pacedKeyToken(key),
			})
		}
	}
	return chunks, nil
}

func pacedKeyToken(key string) bool {
	if key == "Enter" {
		return true
	}
	if len(key) == 3 && (key[0] == 'C' || key[0] == 'c') && key[1] == '-' {
		return true
	}
	return false
}

func cmdSetMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: set-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	kvPairs := ctx.Args[1:]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, kv := range kvPairs {
			key, value, ok := strings.Cut(kv, "=")
			if !ok {
				return commandMutationResult{err: fmt.Errorf("invalid key=value: %q", kv)}
			}
			switch key {
			case "task":
				pane.Meta.Task = value
			case "pr":
				pane.Meta.PR = value
			case "branch":
				if value == "" {
					pane.SetMetaManualBranch(false)
					pane.Meta.GitBranch = ""
				} else {
					pane.Meta.GitBranch = value
					pane.SetMetaManualBranch(true)
				}
			default:
				return commandMutationResult{err: fmt.Errorf("unknown meta key: %q (valid: task, pr, branch)", key)}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdAddMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: add-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	update, err := parseMetaCollectionArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.prs {
			if !slices.Contains(pane.Meta.PRs, pr) {
				pane.Meta.PRs = append(pane.Meta.PRs, pr)
			}
		}
		for _, issue := range update.issues {
			if !slices.Contains(pane.Meta.Issues, issue) {
				pane.Meta.Issues = append(pane.Meta.Issues, issue)
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdRmMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: rm-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	update, err := parseMetaCollectionArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.prs {
			pane.Meta.PRs = removeIntValue(pane.Meta.PRs, pr)
		}
		for _, issue := range update.issues {
			pane.Meta.Issues = removeStringValue(pane.Meta.Issues, issue)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

type metaCollectionUpdate struct {
	prs    []int
	issues []string
}

func parseMetaCollectionArgs(kvPairs []string) (metaCollectionUpdate, error) {
	var update metaCollectionUpdate
	for _, kv := range kvPairs {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			return metaCollectionUpdate{}, fmt.Errorf("invalid key=value: %q", kv)
		}
		switch key {
		case "pr":
			pr, err := parseMetaPR(value)
			if err != nil {
				return metaCollectionUpdate{}, err
			}
			update.prs = append(update.prs, pr)
		case "issue":
			if value == "" {
				return metaCollectionUpdate{}, fmt.Errorf("invalid issue value: %q", value)
			}
			update.issues = append(update.issues, value)
		default:
			return metaCollectionUpdate{}, fmt.Errorf("unknown meta key: %q (valid: pr, issue)", key)
		}
	}
	return update, nil
}

func parseMetaPR(value string) (int, error) {
	pr, err := strconv.Atoi(value)
	if err != nil || pr <= 0 {
		return 0, fmt.Errorf("invalid pr value: %q", value)
	}
	return pr, nil
}

func removeIntValue(values []int, target int) []int {
	return slices.DeleteFunc(values, func(value int) bool {
		return value == target
	})
}

func removeStringValue(values []string, target string) []string {
	return slices.DeleteFunc(values, func(value string) bool {
		return value == target
	})
}

func formatMetaCollections(prs []int, issues []string) string {
	var parts []string
	if len(prs) > 0 {
		values := make([]string, 0, len(prs))
		for _, pr := range prs {
			values = append(values, strconv.Itoa(pr))
		}
		parts = append(parts, "prs=["+strings.Join(values, ",")+"]")
	}
	if len(issues) > 0 {
		parts = append(parts, "issues=["+strings.Join(issues, ",")+"]")
	}
	return strings.Join(parts, " ")
}

func cmdInjectProxy(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: _inject-proxy <host>")
		return
	}
	hostName := ctx.Args[0]
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.ActiveWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		id := sess.counter.Add(1)
		meta := mux.PaneMeta{
			Name:  fmt.Sprintf(mux.PaneNameFormat, id),
			Host:  hostName,
			Color: config.CatppuccinMocha[0], // Rosewater
		}
		proxyPane := mux.NewProxyPaneWithScrollback(id, meta, w.Width/2, mux.PaneContentHeight(w.Height), sess.scrollbackLines,
			sess.paneOutputCallback(),
			sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		sess.Panes = append(sess.Panes, proxyPane)
		if _, err := w.Split(mux.SplitVertical, proxyPane); err != nil {
			sess.removePane(proxyPane.ID)
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Injected proxy pane %s @%s\n", meta.Name, hostName),
			broadcastLayout: true,
		}
	}))
}
