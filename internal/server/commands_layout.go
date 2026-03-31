package server

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
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
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--cleanup", Type: cmdflags.FlagTypeBool},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: defaultKillCleanupTimeout},
	})
	if err != nil {
		return killCommandArgs{}, &killArgError{msg: err.Error()}
	}
	positionals := flags.Positionals()
	if len(positionals) > 1 {
		return killCommandArgs{}, newKillUsageError()
	}
	opts := killCommandArgs{
		cleanup: flags.Bool("--cleanup"),
		timeout: flags.Duration("--timeout"),
	}
	if len(positionals) == 1 {
		opts.paneRef = positionals[0]
	}
	if flags.Seen("--timeout") && !opts.cleanup {
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

type createPanePlacement uint8

const (
	createPanePlacementSplitAt createPanePlacement = iota
	createPanePlacementSpiral
	createPanePlacementRootSplit
)

type createPaneRequest struct {
	paneRef      string
	hostName     string
	hostExplicit bool
	name         string
	task         string
	color        string
	dir          mux.SplitDir
}

type createPaneSnapshot struct {
	windowID     uint32
	windowWidth  int
	windowHeight int
	inheritHost  string
	inheritPID   int
	inheritProxy bool
	targetPaneID uint32
	plan         mux.SpiralAddPlan
}

func runCreatePane(ctx *CommandContext, placement createPanePlacement, keepFocus bool) {
	req, err := parseCreatePaneRequest(ctx)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	snapshot, err := queryCreatePaneSnapshot(ctx, placement, req.paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	switch {
	case ctx.CommandName == "split" && req.hostName == "" && snapshot.inheritProxy:
		req.hostName = snapshot.inheritHost
	case placement == createPanePlacementSpiral && !req.hostExplicit && snapshot.inheritProxy:
		req.hostName = snapshot.inheritHost
	}

	if req.hostName != "" {
		pane, err := ctx.Sess.prepareRemotePane(req.hostName, snapshot.windowWidth, mux.PaneContentHeight(snapshot.windowHeight))
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		applyCreatePaneMeta(&pane.Meta, req)
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			w, err := resolveCreatePaneWindow(sess, ctx.ActorPaneID, placement, snapshot)
			if err != nil {
				pane.Close()
				return commandMutationResult{err: err}
			}
			sess.Panes = append(sess.Panes, pane)
			if err := placeCreatedPaneInWindow(w, placement, snapshot, pane, req.dir, keepFocus); err != nil {
				return cleanupFailedPaneMutation(sess, pane, err)
			}
			return commandMutationResult{
				output:          createPaneOutput(ctx.CommandName, placement, req.dir, pane, req.hostName),
				broadcastLayout: true,
			}
		}))
		return
	}

	meta := mux.PaneMeta{
		Name:  req.name,
		Task:  req.task,
		Color: req.color,
		Dir:   mux.PaneCwd(snapshot.inheritPID),
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w, err := resolveCreatePaneWindow(sess, ctx.ActorPaneID, placement, snapshot)
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane, err := sess.createPaneWithMeta(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := placeCreatedPaneInWindow(w, placement, snapshot, pane, req.dir, keepFocus); err != nil {
			return cleanupFailedPaneMutation(sess, pane, err)
		}
		return commandMutationResult{
			output:          createPaneOutput(ctx.CommandName, placement, req.dir, pane, ""),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func parseCreatePaneRequest(ctx *CommandContext) (createPaneRequest, error) {
	switch ctx.CommandName {
	case "split":
		args, err := layoutcmd.ParseSplitArgs(ctx.Args)
		return createPaneRequest{
			paneRef:  args.PaneRef,
			hostName: args.HostName,
			name:     args.Name,
			task:     args.Task,
			color:    args.Color,
			dir:      args.Dir,
		}, err
	default:
		args, err := layoutcmd.ParseSpawnArgs(ctx.Args)
		hostName := args.Meta.Host
		if hostName == mux.DefaultHost {
			hostName = ""
		}
		return createPaneRequest{
			hostName:     hostName,
			hostExplicit: args.HostExplicit,
			name:         args.Meta.Name,
			task:         args.Meta.Task,
			color:        args.Meta.Color,
			dir:          mux.SplitVertical,
		}, err
	}
}

func queryCreatePaneSnapshot(ctx *CommandContext, placement createPanePlacement, paneRef string) (createPaneSnapshot, error) {
	if placement == createPanePlacementSpiral {
		return enqueueSessionQuery(ctx.Sess, func(sess *Session) (createPaneSnapshot, error) {
			w := sess.windowForActor(ctx.ActorPaneID)
			if w == nil {
				return createPaneSnapshot{}, fmt.Errorf("no window")
			}
			plan, err := w.PlanSpiralAdd()
			if err != nil {
				return createPaneSnapshot{}, err
			}
			inheritPane := sess.findPaneByID(plan.InheritPaneID)
			if inheritPane == nil {
				return createPaneSnapshot{}, fmt.Errorf("pane %d not found", plan.InheritPaneID)
			}
			return createPaneSnapshot{
				windowID:     w.ID,
				windowWidth:  w.Width,
				windowHeight: w.Height,
				inheritHost:  inheritPane.Meta.Host,
				inheritPID:   inheritPane.ProcessPid(),
				inheritProxy: inheritPane.IsProxy(),
				plan:         plan,
			}, nil
		})
	}

	return enqueueSessionQuery(ctx.Sess, func(sess *Session) (createPaneSnapshot, error) {
		w := sess.windowForActor(ctx.ActorPaneID)
		if paneRef != "" {
			pane, resolvedWindow, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
			if err != nil {
				return createPaneSnapshot{}, err
			}
			if resolvedWindow == nil {
				return createPaneSnapshot{}, fmt.Errorf("pane not in any window")
			}
			return createPaneSnapshot{
				windowID:     resolvedWindow.ID,
				windowWidth:  resolvedWindow.Width,
				windowHeight: resolvedWindow.Height,
				inheritHost:  pane.Meta.Host,
				inheritPID:   pane.ProcessPid(),
				inheritProxy: pane.IsProxy(),
				targetPaneID: pane.ID,
			}, nil
		}
		if w == nil {
			return createPaneSnapshot{}, createPaneWindowError(ctx.CommandName)
		}
		if w.ActivePane == nil {
			return createPaneSnapshot{}, fmt.Errorf("no active pane")
		}
		return createPaneSnapshot{
			windowID:     w.ID,
			windowWidth:  w.Width,
			windowHeight: w.Height,
			inheritHost:  w.ActivePane.Meta.Host,
			inheritPID:   w.ActivePane.ProcessPid(),
			inheritProxy: w.ActivePane.IsProxy(),
			targetPaneID: w.ActivePane.ID,
		}, nil
	})
}

func resolveCreatePaneWindow(sess *Session, actorPaneID uint32, placement createPanePlacement, snapshot createPaneSnapshot) (*mux.Window, error) {
	if placement == createPanePlacementSpiral {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return nil, fmt.Errorf("no window")
		}
		if w.ID != snapshot.windowID {
			return nil, fmt.Errorf("window changed during spawn --spiral")
		}
		return w, nil
	}
	w := sess.findWindowByPaneID(snapshot.targetPaneID)
	if w == nil {
		return nil, fmt.Errorf("pane not in any window")
	}
	return w, nil
}

func placeCreatedPaneInWindow(w *mux.Window, placement createPanePlacement, snapshot createPaneSnapshot, pane *mux.Pane, dir mux.SplitDir, keepFocus bool) error {
	opts := mux.SplitOptions{KeepFocus: keepFocus || w.ZoomedPaneID != 0}
	switch placement {
	case createPanePlacementSplitAt:
		_, err := w.SplitPaneWithOptions(snapshot.targetPaneID, dir, pane, opts)
		return err
	case createPanePlacementSpiral:
		_, err := w.ApplySpiralAddPlan(snapshot.plan, pane, opts)
		return err
	case createPanePlacementRootSplit:
		_, err := w.SplitRootWithOptions(dir, pane, opts)
		return err
	default:
		return fmt.Errorf("unknown create-pane placement: %d", placement)
	}
}

func applyCreatePaneMeta(meta *mux.PaneMeta, req createPaneRequest) {
	if req.name != "" {
		meta.Name = req.name
	}
	if req.task != "" {
		meta.Task = req.task
	}
	if req.color != "" {
		meta.Color = req.color
	}
}

func createPaneWindowError(command string) error {
	if command == "split" {
		return fmt.Errorf("no active pane")
	}
	return fmt.Errorf("no window")
}

func createPaneOutput(command string, placement createPanePlacement, dir mux.SplitDir, pane *mux.Pane, hostName string) string {
	switch command {
	case "split":
		if hostName != "" {
			return fmt.Sprintf("Split %s: new remote pane %s @%s\n", dirName(dir), pane.Meta.Name, hostName)
		}
		return fmt.Sprintf("Split %s: new pane %s\n", dirName(dir), pane.Meta.Name)
	default:
		if placement == createPanePlacementSpiral && hostName != "" {
			return fmt.Sprintf("Spawned %s @%s\n", pane.Meta.Name, hostName)
		}
		if hostName != "" {
			return fmt.Sprintf("Spawned %s in pane %d @%s\n", pane.Meta.Name, pane.ID, hostName)
		}
		return fmt.Sprintf("Spawned %s in pane %d\n", pane.Meta.Name, pane.ID)
	}
}

func runSplit(ctx *CommandContext, rawArgs []string) {
	args, err := layoutcmd.ParseSplitArgs(rawArgs)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	placement := createPanePlacementSplitAt
	if args.RootLevel {
		placement = createPanePlacementRootSplit
	}
	runCreatePane(ctx, placement, !args.Focus)
}

func cmdSplit(ctx *CommandContext) {
	runSplit(ctx, ctx.Args)
}

func runSpawn(ctx *CommandContext, rawArgs []string) {
	args, err := layoutcmd.ParseSpawnArgs(rawArgs)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	placement := createPanePlacementSplitAt
	if args.Spiral {
		placement = createPanePlacementSpiral
	}
	runCreatePane(ctx, placement, !args.Focus)
}

func cmdSpawn(ctx *CommandContext) {
	runSpawn(ctx, ctx.Args)
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
		return clearedPaneRenderResult(fmt.Sprintf("Reset %s\n", pane.Meta.Name), pane, w != nil, nil, nil)
	}))
}

func cmdRespawn(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, w, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "respawn", ctx.Args)
		if err != nil {
			return commandMutationResult{err: err}
		}

		newPane, err := sess.respawnPane(ctx.Srv, pane, w)
		if err != nil {
			return commandMutationResult{err: err}
		}

		return clearedPaneRenderResult(
			fmt.Sprintf("Respawned %s\n", newPane.Meta.Name),
			newPane,
			true,
			[]*mux.Pane{newPane},
			[]*mux.Pane{pane},
		)
	}))
}

func clearedPaneRenderResult(output string, pane *mux.Pane, includeRender bool, startPanes, closePanes []*mux.Pane) commandMutationResult {
	res := commandMutationResult{
		output: output,
		paneHistories: []paneHistoryUpdate{{
			paneID:  pane.ID,
			history: nil,
		}},
		startPanes: startPanes,
		closePanes: closePanes,
	}
	if includeRender {
		res.paneRenders = []paneRender{{
			paneID: pane.ID,
			data:   append([]byte("\x1bc"), []byte(pane.RenderScreen())...),
		}}
	}
	return res
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
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--wait", Type: cmdflags.FlagTypeString},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: defaultCommandUIWaitTimeout},
	})
	if err != nil {
		return copyModeOptions{}, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 1 {
		return copyModeOptions{}, fmt.Errorf(copyModeUsage)
	}
	opts := copyModeOptions{waitTimeout: flags.Duration("--timeout")}
	if len(positionals) == 1 {
		opts.paneRef = positionals[0]
	}
	if flags.Seen("--wait") {
		target := flags.String("--wait")
		if target != "ui=copy-mode-shown" {
			return copyModeOptions{}, fmt.Errorf("copy-mode: unsupported --wait target %q (want ui=copy-mode-shown)", target)
		}
		opts.waitCopyModeShown = true
	}

	if flags.Seen("--timeout") && !opts.waitCopyModeShown {
		return copyModeOptions{}, fmt.Errorf("copy-mode: --timeout requires --wait ui=copy-mode-shown")
	}

	return opts, nil
}

func parseEqualizeCommandArgs(args []string) (widths, heights bool, err error) {
	flags, err := cmdflags.ParseCommandFlags(args, []cmdflags.FlagSpec{
		{Name: "--vertical", Type: cmdflags.FlagTypeBool},
		{Name: "--all", Type: cmdflags.FlagTypeBool},
	})
	if err != nil {
		return false, false, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 0 {
		return false, false, fmt.Errorf(`equalize: unknown mode %q (use --vertical or --all)`, positionals[0])
	}
	if flags.Bool("--vertical") && flags.Bool("--all") {
		return false, false, fmt.Errorf("equalize: conflicting equalize modes")
	}
	if flags.Bool("--all") {
		return true, true, nil
	}
	if flags.Bool("--vertical") {
		return false, true, nil
	}
	return true, false, nil
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
