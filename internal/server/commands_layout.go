package server

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"
)

const (
	killArgsUsage             = "[--cleanup] [--timeout <duration>] [pane]"
	defaultKillCleanupTimeout = 5 * time.Second
	copyModeUsage             = "usage: copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]"
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

func keepFocusOnCreate(w *mux.Window, focus bool) bool {
	return !focus || w.ZoomedPaneID != 0
}

func runSplit(ctx *CommandContext, rawArgs []string) {
	args, err := layoutcmd.ParseSplitArgs(rawArgs)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	var resolved resolvedPaneRef
	if args.PaneRef == "" {
		resolved, err = enqueueSessionQuery(ctx.Sess, func(sess *Session) (resolvedPaneRef, error) {
			w := sess.windowForActor(ctx.ActorPaneID)
			if w == nil || w.ActivePane == nil {
				return resolvedPaneRef{}, fmt.Errorf("no active pane")
			}
			return resolvedPaneRef{
				pane:     w.ActivePane,
				paneID:   w.ActivePane.ID,
				paneName: w.ActivePane.Meta.Name,
				windowID: w.ID,
			}, nil
		})
	} else {
		resolved, err = ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, args.PaneRef)
	}
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	if args.HostName == "" && resolved.pane.IsProxy() {
		args.HostName = resolved.pane.Meta.Host
	}

	if args.HostName != "" {
		pane, err := ctx.CC.splitRemotePane(ctx.Sess, args.HostName, args.Dir, args.RootLevel, args.Name, !args.Focus)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			registered := sess.findPaneByID(pane.ID)
			if registered == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", pane.Meta.Name)}
			}
			if args.Task != "" {
				registered.Meta.Task = args.Task
			}
			if args.Color != "" {
				registered.Meta.Color = args.Color
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Split %s: new remote pane %s @%s\n", dirName(args.Dir), pane.Meta.Name, args.HostName),
				broadcastLayout: args.Task != "" || args.Color != "",
			}
		}))
		return
	}

	targetPaneID := resolved.paneID
	meta := mux.PaneMeta{
		Name:  args.Name,
		Task:  args.Task,
		Color: args.Color,
		Dir:   mux.PaneCwd(resolved.pane.ProcessPid()),
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.findWindowByPaneID(targetPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("pane not in any window")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		opts := mux.SplitOptions{KeepFocus: keepFocusOnCreate(w, args.Focus)}
		if args.RootLevel {
			_, err = w.SplitRootWithOptions(args.Dir, pane, opts)
		} else {
			_, err = w.SplitPaneWithOptions(targetPaneID, args.Dir, pane, opts)
		}
		if err != nil {
			return cleanupFailedPaneMutation(sess, pane, err)
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Split %s: new pane %s\n", dirName(args.Dir), pane.Meta.Name),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdSplit(ctx *CommandContext) {
	runSplit(ctx, ctx.Args)
}

type spiralSpawnSnapshot struct {
	windowID     uint32
	windowWidth  int
	windowHeight int
	inheritHost  string
	inheritPID   int
	inheritProxy bool
	plan         mux.SpiralAddPlan
}

func runSpiralSpawn(ctx *CommandContext, args layoutcmd.SpawnArgs) {
	snapshot, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (spiralSpawnSnapshot, error) {
		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return spiralSpawnSnapshot{}, fmt.Errorf("no window")
		}
		plan, err := w.PlanSpiralAdd()
		if err != nil {
			return spiralSpawnSnapshot{}, err
		}
		inheritPane := sess.findPaneByID(plan.InheritPaneID)
		if inheritPane == nil {
			return spiralSpawnSnapshot{}, fmt.Errorf("pane %d not found", plan.InheritPaneID)
		}
		return spiralSpawnSnapshot{
			windowID:     w.ID,
			windowWidth:  w.Width,
			windowHeight: w.Height,
			inheritHost:  inheritPane.Meta.Host,
			inheritPID:   inheritPane.ProcessPid(),
			inheritProxy: inheritPane.IsProxy(),
			plan:         plan,
		}, nil
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	remoteHost := ""
	if args.HostExplicit {
		if args.Meta.Host != "" && args.Meta.Host != mux.DefaultHost {
			remoteHost = args.Meta.Host
		}
	} else if snapshot.inheritProxy {
		remoteHost = snapshot.inheritHost
	}

	if remoteHost != "" {
		pane, err := ctx.Sess.prepareRemotePane(remoteHost, snapshot.windowWidth, mux.PaneContentHeight(snapshot.windowHeight))
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if args.Meta.Name != "" {
			pane.Meta.Name = args.Meta.Name
		}
		if args.Meta.Task != "" {
			pane.Meta.Task = args.Meta.Task
		}
		if args.Meta.Color != "" {
			pane.Meta.Color = args.Meta.Color
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			w := sess.windowForActor(ctx.ActorPaneID)
			if w == nil || w.ID != snapshot.windowID {
				if w == nil {
					return commandMutationResult{err: fmt.Errorf("no window")}
				}
				return commandMutationResult{err: fmt.Errorf("window changed during spawn --spiral")}
			}
			sess.Panes = append(sess.Panes, pane)
			if _, err := w.ApplySpiralAddPlan(snapshot.plan, pane, mux.SplitOptions{KeepFocus: keepFocusOnCreate(w, args.Focus)}); err != nil {
				return cleanupFailedPaneMutation(sess, pane, err)
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Spawned %s @%s\n", pane.Meta.Name, remoteHost),
				broadcastLayout: true,
			}
		}))
		return
	}

	if args.Meta.Dir == "" {
		args.Meta.Dir = mux.PaneCwd(snapshot.inheritPID)
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil || w.ID != snapshot.windowID {
			if w == nil {
				return commandMutationResult{err: fmt.Errorf("no window")}
			}
			return commandMutationResult{err: fmt.Errorf("window changed during spawn --spiral")}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, args.Meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if _, err := w.ApplySpiralAddPlan(snapshot.plan, pane, mux.SplitOptions{KeepFocus: keepFocusOnCreate(w, args.Focus)}); err != nil {
			return cleanupFailedPaneMutation(sess, pane, err)
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Spawned %s in pane %d\n", pane.Meta.Name, pane.ID),
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
				sess.activateWindow(pw)
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

func runSpawn(ctx *CommandContext, rawArgs []string) {
	args, err := layoutcmd.ParseSpawnArgs(rawArgs)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if args.Spiral {
		runSpiralSpawn(ctx, args)
		return
	}

	remoteHost := args.Meta.Host
	if remoteHost != "" && remoteHost != mux.DefaultHost {
		pane, err := ctx.CC.splitRemotePane(ctx.Sess, remoteHost, mux.SplitVertical, false, args.Meta.Name, !args.Focus)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			registered := sess.findPaneByID(pane.ID)
			if registered == nil {
				return commandMutationResult{err: fmt.Errorf("pane %q not found", pane.Meta.Name)}
			}
			if args.Meta.Name != "" {
				registered.Meta.Name = args.Meta.Name
			}
			if args.Meta.Task != "" {
				registered.Meta.Task = args.Meta.Task
			}
			if args.Meta.Color != "" {
				registered.Meta.Color = args.Meta.Color
			}
			return commandMutationResult{
				output:          fmt.Sprintf("Spawned %s in pane %d @%s\n", registered.Meta.Name, pane.ID, remoteHost),
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
		opts := mux.SplitOptions{KeepFocus: keepFocusOnCreate(w, args.Focus)}
		_, err = w.SplitWithOptions(mux.SplitVertical, pane, opts)
		if err != nil {
			return cleanupFailedPaneMutation(sess, pane, err)
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Spawned %s in pane %d\n", pane.Meta.Name, pane.ID),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdSpawn(ctx *CommandContext) {
	runSpawn(ctx, ctx.Args)
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
			var err error
			pane, err = w.ResolvePane(ctx.Args[0])
			if err != nil {
				return commandMutationResult{err: err}
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

func cmdReset(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if len(ctx.Args) < 1 {
			return commandMutationResult{err: fmt.Errorf("usage: reset <pane>")}
		}
		pane, w, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, ctx.Args[0])
		if err != nil {
			return commandMutationResult{err: err}
		}

		pane.ResetState()

		res := commandMutationResult{
			output: fmt.Sprintf("Reset %s\n", pane.Meta.Name),
			paneHistories: []paneHistoryUpdate{{
				paneID:  pane.ID,
				history: nil,
			}},
		}
		if w != nil {
			res.paneRenders = []paneRender{{
				paneID: pane.ID,
				data:   append([]byte("\x1bc"), []byte(pane.RenderScreen())...),
			}}
		}
		return res
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

		removed := sess.softClosePane(pane.ID)
		if removed.pane == nil {
			return commandMutationResult{}
		}

		sess.appendPaneLog(paneLogEventExit, removed.pane, "killed")
		sess.emitEvent(Event{
			Type:     EventPaneExit,
			PaneID:   pane.ID,
			PaneName: removed.paneName,
			Host:     removed.pane.Meta.Host,
			Reason:   "killed",
		})

		// If the last pane was killed, session exits — no undo possible.
		if removed.sendExit {
			return commandMutationResult{
				closePanes:     []*mux.Pane{removed.pane},
				output:         fmt.Sprintf("Killed %s (session exiting)\n", removed.paneName),
				sendExit:       true,
				shutdownServer: true,
			}
		}

		res := commandMutationResult{
			broadcastLayout: removed.broadcastLayout,
		}
		if removed.closedWindow != "" {
			res.output = fmt.Sprintf("Killed %s (closed %s)\n", removed.paneName, removed.closedWindow)
		} else {
			res.output = fmt.Sprintf("Killed %s\n", removed.paneName)
		}
		return res
	}))
}

func cmdUndo(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, err := sess.undoClosePane()
		if err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Restored %s\n", pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdCopyMode(ctx *CommandContext) {
	opts, err := parseCopyModeArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	var pane resolvedPaneRef
	if opts.paneRef == "" {
		pane, err = enqueueSessionQuery(ctx.Sess, func(sess *Session) (resolvedPaneRef, error) {
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
	} else {
		pane, err = ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, opts.paneRef)
	}
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	var uiWait uiClientSnapshot
	if opts.waitCopyModeShown {
		uiWait, err = ctx.Sess.queryUIClient("", proto.UIEventCopyModeShown)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
	}

	ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: pane.paneID})
	if opts.waitCopyModeShown {
		if err := waitForNextUIEvent(ctx.Sess, uiWait, proto.UIEventCopyModeShown, opts.waitTimeout); err != nil {
			ctx.replyErr(err.Error())
			return
		}
	}
	ctx.reply(fmt.Sprintf("Copy mode entered for %s\n", pane.paneName))
}

type copyModeOptions struct {
	paneRef           string
	waitCopyModeShown bool
	waitTimeout       time.Duration
}

func parseCopyModeArgs(args []string) (copyModeOptions, error) {
	opts := copyModeOptions{waitTimeout: defaultCommandUIWaitTimeout}
	timeoutSet := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--wait":
			if i+1 >= len(args) {
				return copyModeOptions{}, fmt.Errorf("missing value for --wait")
			}
			i++
			if args[i] != "ui=copy-mode-shown" {
				return copyModeOptions{}, fmt.Errorf("copy-mode: unsupported --wait target %q (want ui=copy-mode-shown)", args[i])
			}
			opts.waitCopyModeShown = true
		case "--timeout":
			if i+1 >= len(args) {
				return copyModeOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return copyModeOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			timeoutSet = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return copyModeOptions{}, fmt.Errorf("unknown flag: %s", args[i])
			}
			if opts.paneRef != "" {
				return copyModeOptions{}, fmt.Errorf(copyModeUsage)
			}
			opts.paneRef = args[i]
		}
	}

	if timeoutSet && !opts.waitCopyModeShown {
		return copyModeOptions{}, fmt.Errorf("copy-mode: --timeout requires --wait ui=copy-mode-shown")
	}

	return opts, nil
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
		newWin.LeadPaneID = pane.ID
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
		sess.activateWindow(w)
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
			if w.ActivePane != nil && w.IsLeadPane(w.ActivePane.ID) {
				return commandMutationResult{err: fmt.Errorf("cannot operate on lead pane")}
			}
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
		if w.IsLeadPane(p.ID) {
			return commandMutationResult{err: fmt.Errorf("cannot operate on lead pane")}
		}
		w.ResizePane(p.ID, direction, delta)
		return commandMutationResult{
			output:          fmt.Sprintf("Resized %s %s by %d\n", p.Meta.Name, direction, delta),
			broadcastLayout: true,
		}
	}))
}

func parseEqualizeCommandArgs(args []string) (widths, heights bool, err error) {
	mode := ""
	for _, arg := range args {
		switch arg {
		case "--vertical":
			if mode != "" && mode != arg {
				return false, false, fmt.Errorf("equalize: conflicting equalize modes")
			}
			mode = arg
		case "--all":
			if mode != "" && mode != arg {
				return false, false, fmt.Errorf("equalize: conflicting equalize modes")
			}
			mode = arg
		default:
			return false, false, fmt.Errorf(`equalize: unknown mode %q (use --vertical or --all)`, arg)
		}
	}
	switch mode {
	case "":
		return true, false, nil
	case "--vertical":
		return false, true, nil
	case "--all":
		return true, true, nil
	default:
		return false, false, fmt.Errorf(`equalize: unknown mode %q (use --vertical or --all)`, mode)
	}
}

func cmdEqualize(ctx *CommandContext) {
	widths, heights, err := parseEqualizeCommandArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		changed := w.Equalize(widths, heights)
		output := "Already equalized\n"
		if changed {
			output = "Equalized layout\n"
		}
		return commandMutationResult{
			output:          output,
			broadcastLayout: changed,
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

func cmdSetLead(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		pane := w.ActivePane
		if len(ctx.Args) > 0 {
			resolved, err := w.ResolvePane(ctx.Args[0])
			if err != nil {
				return commandMutationResult{err: err}
			}
			pane = resolved
		}
		if pane == nil {
			return commandMutationResult{err: fmt.Errorf("no active pane")}
		}
		if err := w.SetLead(pane.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Set lead: %s\n", pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdUnsetLead(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		if err := w.UnsetLead(); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          "Lead cleared\n",
			broadcastLayout: true,
		}
	}))
}

func cmdToggleLead(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		if w.ActivePane == nil {
			return commandMutationResult{err: fmt.Errorf("no active pane")}
		}
		if w.IsLeadPane(w.ActivePane.ID) {
			if err := w.UnsetLead(); err != nil {
				return commandMutationResult{err: err}
			}
			return commandMutationResult{
				output:          "Lead cleared\n",
				broadcastLayout: true,
			}
		}
		if err := w.SetLead(w.ActivePane.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Set lead: %s\n", w.ActivePane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}
