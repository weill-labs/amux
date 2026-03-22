package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/render"
	layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"
)

const (
	killArgsUsage             = "[--cleanup] [--timeout <duration>] [pane]"
	defaultKillCleanupTimeout = 5 * time.Second
)

type killArgError struct {
	msg   string
	usage bool
}

func (e *killArgError) Error() string { return e.msg }

type killCommandArgs struct {
	paneRef string
	cleanup bool
	timeout time.Duration
}

func newKillUsageError() error {
	return &killArgError{msg: KillCommandUsage(""), usage: true}
}

// KillCommandUsage formats the user-facing usage string for the kill command.
func KillCommandUsage(command string) string {
	if command == "" {
		return fmt.Sprintf("usage: kill %s", killArgsUsage)
	}
	return fmt.Sprintf("usage: %s kill %s", command, killArgsUsage)
}

// ValidateKillCommandArgs validates kill CLI arguments without mutating state.
func ValidateKillCommandArgs(args []string) error {
	_, err := parseKillCommandArgs(args)
	return err
}

// FormatKillCommandError rewrites usage errors for the requested command name.
func FormatKillCommandError(err error, command string) string {
	var argErr *killArgError
	if errors.As(err, &argErr) && argErr.usage {
		return KillCommandUsage(command)
	}
	return err.Error()
}

func parseKillCommandArgs(args []string) (killCommandArgs, error) {
	opts := killCommandArgs{timeout: defaultKillCleanupTimeout}
	timeoutSet := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--cleanup":
			opts.cleanup = true
		case "--timeout":
			if i+1 >= len(args) {
				return killCommandArgs{}, newKillUsageError()
			}
			timeout, err := time.ParseDuration(args[i+1])
			if err != nil {
				return killCommandArgs{}, &killArgError{msg: fmt.Sprintf("invalid timeout: %s", args[i+1])}
			}
			opts.timeout = timeout
			timeoutSet = true
			i++
		default:
			if strings.HasPrefix(arg, "--") {
				return killCommandArgs{}, &killArgError{msg: fmt.Sprintf("unknown flag: %s", arg)}
			}
			if opts.paneRef != "" {
				return killCommandArgs{}, newKillUsageError()
			}
			opts.paneRef = arg
		}
	}
	if timeoutSet && !opts.cleanup {
		return killCommandArgs{}, newKillUsageError()
	}
	return opts, nil
}

func dirName(d mux.SplitDir) string {
	return layoutcmd.DirName(d)
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

func cmdSplit(ctx *CommandContext) {
	args, err := layoutcmd.ParseSplitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if args.HostName == "" {
		snap, err := ctx.Sess.queryActiveWindowSnapshot()
		if err == nil {
			args.HostName = snap.proxyHost
		}
	}

	if args.HostName != "" {
		pane, err := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, args.HostName, args.Dir, args.RootLevel, args.Name, args.Background)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.reply(fmt.Sprintf("Split %s: new remote pane %s @%s\n", dirName(args.Dir), pane.Meta.Name, args.HostName))
		return
	}

	activePid, _, _, err := ctx.activeWindowSnapshot()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	meta := mux.PaneMeta{Name: args.Name, Dir: mux.PaneCwd(activePid)}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		opts := mux.SplitOptions{Background: args.Background || w.ZoomedPaneID != 0}
		if args.RootLevel {
			_, err = w.SplitRootWithOptions(args.Dir, pane, opts)
		} else {
			_, err = w.SplitWithOptions(args.Dir, pane, opts)
		}
		if err != nil {
			sess.removePane(pane.ID)
			pane.Close()
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Split %s: new pane %s\n", dirName(args.Dir), pane.Meta.Name),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdFocus(ctx *CommandContext) {
	direction := "next"
	if len(ctx.Args) > 0 {
		direction = ctx.Args[0]
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
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
			pane, pw, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, direction)
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

func cmdSpawn(ctx *CommandContext) {
	args, err := layoutcmd.ParseSpawnArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	remoteHost := args.Meta.Host
	if remoteHost != "" && remoteHost != mux.DefaultHost {
		pane, err := ctx.CC.splitRemotePane(ctx.Srv, ctx.Sess, remoteHost, mux.SplitVertical, false, args.Meta.Name, args.Background)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			registered := sess.findPaneByID(pane.ID)
			if registered == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", pane.Meta.Name)}
			}
			registered.Meta.Name = args.Meta.Name
			if args.Meta.Task != "" {
				registered.Meta.Task = args.Meta.Task
			}
			if args.Meta.Color != "" {
				registered.Meta.Color = args.Meta.Color
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Spawned %s in pane %d @%s\n", args.Meta.Name, pane.ID, remoteHost),
				broadcastLayout: true,
			}
		}))
		return
	}

	activePid, _, _, err := ctx.activeWindowSnapshot()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if args.Meta.Dir == "" {
		args.Meta.Dir = mux.PaneCwd(activePid)
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, args.Meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if _, err := w.SplitWithOptions(mux.SplitVertical, pane, mux.SplitOptions{Background: args.Background || w.ZoomedPaneID != 0}); err != nil {
			sess.removePane(pane.ID)
			pane.Close()
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Spawned %s in pane %d\n", args.Meta.Name, pane.ID),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdZoom(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if len(ctx.Args) > 0 {
			// When zooming a named pane, resolve from the actor's window.
			// Zoom without args always toggles in the active window.
			w = sess.windowForActor(ctx.ActorPaneID)
		}
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
		p, w, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "minimize", ctx.Args)
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
		p, w, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "restore", ctx.Args)
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
		w := sess.activeWindow()
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
	opts, err := parseKillCommandArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	target, err := ctx.Sess.queryKillTarget(ctx.ActorPaneID, opts.paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if target.paneID == 0 {
		ctx.replyCommandMutation(commandMutationResult{})
		return
	}

	if target.proxy && ctx.Sess.RemoteManager != nil {
		if err := ctx.Sess.RemoteManager.KillPane(target.paneID, opts.cleanup, opts.timeout); err != nil {
			ctx.replyErr(err.Error())
			return
		}
		verb := "Killed"
		if opts.cleanup {
			verb = "Cleaning up"
		}
		ctx.reply(fmt.Sprintf("%s %s\n", verb, target.paneName))
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane := sess.findPaneByID(target.paneID)
		if pane == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", target.paneName)}
		}
		if opts.cleanup {
			if err := sess.beginPaneCleanupKill(pane, opts.timeout); err != nil {
				return commandMutationResult{err: err}
			}
			return commandMutationResult{
				output: fmt.Sprintf("Cleaning up %s\n", pane.Meta.Name),
			}
		}

		removed := sess.finalizePaneRemoval(pane.ID)
		if removed.pane == nil {
			return commandMutationResult{}
		}

		res := commandMutationResult{
			closePanes: []*mux.Pane{removed.pane},
		}
		if removed.sendExit {
			res.output = fmt.Sprintf("Killed %s (session exiting)\n", removed.paneName)
			res.sendExit = true
			res.shutdownServer = true
			return res
		}

		res.broadcastLayout = removed.broadcastLayout
		if removed.closedWindow != "" {
			res.output = fmt.Sprintf("Killed %s (closed %s)\n", removed.paneName, removed.closedWindow)
		} else {
			res.output = fmt.Sprintf("Killed %s\n", removed.paneName)
		}
		return res
	}))
}

func cmdCopyMode(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (resolvedPaneRef, error) {
			w := sess.activeWindow()
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
	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: pane.paneID})
	ctx.reply(fmt.Sprintf("Copy mode entered for %s\n", pane.paneName))
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
		w := sess.activeWindow()
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

func cmdSelectWindow(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: select-window <index|name>")
		return
	}
	ref := ctx.Args[0]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.resolveWindow(ref)
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
		sess.nextWindow()
		return commandMutationResult{
			output:          "Next window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.activeWindow()),
		}
	}))
}

func cmdPrevWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		sess.prevWindow()
		return commandMutationResult{
			output:          "Previous window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(sess.activeWindow()),
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
		w := sess.activeWindow()
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
		if w := sess.activeWindow(); w != nil {
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
		if w := sess.activeWindow(); w != nil {
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
		p, w, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "resize-pane", ctx.Args[:1])
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
