package server

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/render"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"
)

const (
	copyModeUsage = "usage: copy-mode [pane] [--wait ui=copy-mode-shown] [--timeout <duration>]"
)

type killCommandArgs struct {
	paneRef string
	cleanup bool
	timeout time.Duration
}

// KillCommandUsage formats the user-facing usage string for the kill command.
func KillCommandUsage(command string) string {
	return layoutcmd.KillCommandUsage(command)
}

// ValidateKillCommandArgs validates kill CLI arguments without mutating state.
func ValidateKillCommandArgs(args []string) error {
	return layoutcmd.ValidateKillCommandArgs(args)
}

// FormatKillCommandError rewrites usage errors for the requested command name.
func FormatKillCommandError(err error, command string) string {
	return layoutcmd.FormatKillCommandError(err, command)
}

func parseKillCommandArgs(args []string) (killCommandArgs, error) {
	parsed, err := layoutcmd.ParseKillCommandArgs(args)
	if err != nil {
		return killCommandArgs{}, err
	}
	return killCommandArgs{
		paneRef: parsed.PaneRef,
		cleanup: parsed.Cleanup,
		timeout: parsed.Timeout,
	}, nil
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

func runCreatePane(ctx *CommandContext, actorPaneID uint32, command string, placement createPanePlacement, req createPaneRequest, keepFocus bool) commandpkg.Result {
	snapshot, err := queryCreatePaneSnapshot(ctx.Sess, actorPaneID, command, placement, req.paneRef)
	if err != nil {
		return commandpkg.Result{Err: err}
	}

	switch {
	case command == "split" && req.hostName == "" && snapshot.inheritProxy:
		req.hostName = snapshot.inheritHost
	case placement == createPanePlacementSpiral && !req.hostExplicit && snapshot.inheritProxy:
		req.hostName = snapshot.inheritHost
	}

	if req.hostName != "" {
		pane, err := ctx.Sess.prepareRemotePane(req.hostName, snapshot.windowWidth, mux.PaneContentHeight(snapshot.windowHeight))
		if err != nil {
			return commandpkg.Result{Err: err}
		}
		applyCreatePaneMeta(&pane.Meta, req)
		return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
			w, err := resolveCreatePaneWindow(mctx, actorPaneID, placement, snapshot)
			if err != nil {
				return cleanupFailedPreparedPaneMutationContext(mctx, pane, err)
			}
			mctx.Panes = append(mctx.Panes, pane)
			if err := placeCreatedPaneInWindow(w, placement, snapshot, pane, req.dir, keepFocus); err != nil {
				return cleanupFailedPaneMutationContext(mctx, pane, err)
			}
			return commandMutationResult{
				output:          createPaneOutput(command, placement, req.dir, pane, req.hostName),
				broadcastLayout: true,
			}
		}))
	}

	meta := mux.PaneMeta{
		Name:  req.name,
		Task:  req.task,
		Color: req.color,
		Dir:   mux.PaneCwd(snapshot.inheritPID),
	}
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w, err := resolveCreatePaneWindow(mctx, actorPaneID, placement, snapshot)
		if err != nil {
			return commandMutationResult{err: err}
		}
		prepared, err := mctx.preparePendingLocalPane(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height), "")
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane := prepared.pane
		if err := placeCreatedPaneInWindow(w, placement, snapshot, pane, req.dir, keepFocus); err != nil {
			return cleanupFailedPaneMutationContext(mctx, pane, err)
		}
		mctx.startPendingLocalPaneBuild(ctx.Srv, pane, prepared.build)
		return commandMutationResult{
			output:          createPaneOutput(command, placement, req.dir, pane, ""),
			broadcastLayout: true,
		}
	}))
}

func queryCreatePaneSnapshot(sess *Session, actorPaneID uint32, command string, placement createPanePlacement, paneRef string) (createPaneSnapshot, error) {
	if placement == createPanePlacementSpiral {
		return enqueueSessionQuery(sess, func(sess *Session) (createPaneSnapshot, error) {
			w := sess.windowForActor(actorPaneID)
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

	return enqueueSessionQuery(sess, func(sess *Session) (createPaneSnapshot, error) {
		w := sess.windowForActor(actorPaneID)
		if paneRef != "" {
			pane, resolvedWindow, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
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
			return createPaneSnapshot{}, createPaneWindowError(command)
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

func resolveCreatePaneWindow(ctx *MutationContext, actorPaneID uint32, placement createPanePlacement, snapshot createPaneSnapshot) (*mux.Window, error) {
	if placement == createPanePlacementSpiral {
		w := ctx.windowForActor(actorPaneID)
		if w == nil {
			return nil, fmt.Errorf("no window")
		}
		if w.ID != snapshot.windowID {
			return nil, fmt.Errorf("window changed during spawn --spiral")
		}
		return w, nil
	}
	w := ctx.findWindowByPaneID(snapshot.targetPaneID)
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

type layoutCommandContext struct {
	*CommandContext
}

func (ctx layoutCommandContext) Split(actorPaneID uint32, args layoutcmd.SplitArgs) commandpkg.Result {
	return runSplit(ctx.CommandContext, actorPaneID, args)
}

func (ctx layoutCommandContext) Focus(actorPaneID uint32, direction string) commandpkg.Result {
	return runFocus(ctx.CommandContext, actorPaneID, direction)
}

func (ctx layoutCommandContext) Spawn(actorPaneID uint32, args layoutcmd.SpawnArgs) commandpkg.Result {
	return runSpawn(ctx.CommandContext, actorPaneID, args)
}

func (ctx layoutCommandContext) Zoom(actorPaneID uint32, paneRef string) commandpkg.Result {
	return runZoom(ctx.CommandContext, actorPaneID, paneRef)
}

func (ctx layoutCommandContext) Reset(actorPaneID uint32, paneRef string) commandpkg.Result {
	return runReset(ctx.CommandContext, actorPaneID, paneRef)
}

func (ctx layoutCommandContext) Kill(actorPaneID uint32, args layoutcmd.KillArgs) commandpkg.Result {
	return runKill(ctx.CommandContext, actorPaneID, killCommandArgs{
		paneRef: args.PaneRef,
		cleanup: args.Cleanup,
		timeout: args.Timeout,
	})
}

func (ctx layoutCommandContext) Undo() commandpkg.Result {
	return runUndo(ctx.CommandContext)
}

func (ctx layoutCommandContext) CopyMode(actorPaneID uint32, opts layoutcmd.CopyModeOptions) commandpkg.Result {
	return runCopyMode(ctx.CommandContext, actorPaneID, copyModeOptions{
		paneRef:           opts.PaneRef,
		waitCopyModeShown: opts.WaitCopyModeShown,
		waitTimeout:       opts.WaitTimeout,
	})
}

func (ctx layoutCommandContext) NewWindow(name string) commandpkg.Result {
	return runNewWindow(ctx.CommandContext, name)
}

func (ctx layoutCommandContext) SelectWindow(ref string) commandpkg.Result {
	return runSelectWindow(ctx.CommandContext, ref)
}

func (ctx layoutCommandContext) NextWindow() commandpkg.Result {
	return runNextWindow(ctx.CommandContext)
}

func (ctx layoutCommandContext) PrevWindow() commandpkg.Result {
	return runPrevWindow(ctx.CommandContext)
}

func (ctx layoutCommandContext) RenameWindow(name string) commandpkg.Result {
	return runRenameWindow(ctx.CommandContext, name)
}

func (ctx layoutCommandContext) ResizeBorder(x, y, delta int) commandpkg.Result {
	return runResizeBorder(ctx.CommandContext, x, y, delta)
}

func (ctx layoutCommandContext) ResizeActive(direction string, delta int) commandpkg.Result {
	return runResizeActive(ctx.CommandContext, direction, delta)
}

func (ctx layoutCommandContext) ResizePane(actorPaneID uint32, paneRef, direction string, delta int) commandpkg.Result {
	return runResizePane(ctx.CommandContext, actorPaneID, paneRef, direction, delta)
}

func (ctx layoutCommandContext) Equalize(widths, heights bool) commandpkg.Result {
	return runEqualize(ctx.CommandContext, widths, heights)
}

func (ctx layoutCommandContext) ResizeWindow(cols, rows int) commandpkg.Result {
	return runResizeWindow(ctx.CommandContext, cols, rows)
}

func (ctx layoutCommandContext) SetLead(actorPaneID uint32, paneRef string) commandpkg.Result {
	return runSetLead(ctx.CommandContext, actorPaneID, paneRef)
}

func (ctx layoutCommandContext) UnsetLead(actorPaneID uint32) commandpkg.Result {
	return runUnsetLead(ctx.CommandContext, actorPaneID)
}

func (ctx layoutCommandContext) ToggleLead(actorPaneID uint32) commandpkg.Result {
	return runToggleLead(ctx.CommandContext, actorPaneID)
}

func createPaneRequestFromSplitArgs(args layoutcmd.SplitArgs) createPaneRequest {
	return createPaneRequest{
		paneRef:  args.PaneRef,
		hostName: args.HostName,
		name:     args.Name,
		task:     args.Task,
		color:    args.Color,
		dir:      args.Dir,
	}
}

func createPaneRequestFromSpawnArgs(args layoutcmd.SpawnArgs) createPaneRequest {
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
	}
}

func runSplit(ctx *CommandContext, actorPaneID uint32, args layoutcmd.SplitArgs) commandpkg.Result {
	placement := createPanePlacementSplitAt
	if args.RootLevel {
		placement = createPanePlacementRootSplit
	}
	return runCreatePane(ctx, actorPaneID, "split", placement, createPaneRequestFromSplitArgs(args), !args.Focus)
}

func cmdSplit(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Split(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runFocus(ctx *CommandContext, actorPaneID uint32, direction string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
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
			pane, pw, err := mctx.resolvePaneAcrossWindowsForActor(actorPaneID, direction)
			if err != nil {
				return commandMutationResult{err: err}
			}
			if pw != nil {
				mctx.activateWindow(pw)
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

func cmdFocus(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Focus(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runSpawn(ctx *CommandContext, actorPaneID uint32, args layoutcmd.SpawnArgs) commandpkg.Result {
	placement := createPanePlacementSplitAt
	if args.Spiral {
		placement = createPanePlacementSpiral
	}
	return runCreatePane(ctx, actorPaneID, "spawn", placement, createPaneRequestFromSpawnArgs(args), !args.Focus)
}

func cmdSpawn(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Spawn(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runZoom(ctx *CommandContext, actorPaneID uint32, paneRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
		if paneRef != "" {
			// When zooming a named pane, resolve from the actor's window.
			// Zoom without args always toggles in the active window.
			w = mctx.windowForActor(actorPaneID)
		}
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		var pane *mux.Pane
		if paneRef != "" {
			var err error
			pane, err = w.ResolvePane(paneRef)
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

func cmdZoom(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Zoom(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runReset(ctx *CommandContext, actorPaneID uint32, paneRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		pane, w, err := mctx.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}

		pane.ResetState()
		return clearedPaneRenderResult(fmt.Sprintf("Reset %s\n", pane.Meta.Name), pane, w != nil, nil, nil)
	}))
}

func cmdRespawn(ctx *CommandContext) {
	target, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (struct {
		pane *mux.Pane
	}, error) {
		pane, _, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "respawn", ctx.Args)
		if err != nil {
			return struct {
				pane *mux.Pane
			}{}, err
		}
		return struct {
			pane *mux.Pane
		}{pane: pane}, nil
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if target.pane.IsProxy() {
		ctx.replyErr("cannot respawn proxy pane")
		return
	}

	newPane, err := ctx.Sess.buildConfiguredLocalPane(ctx.Srv, localPaneBuildRequest{
		sourcePane:   target.pane,
		sessionName:  ctx.Sess.Name,
		colorProfile: ctx.Sess.paneLaunchColorProfile(nil),
		startDir:     effectiveRespawnDir(target.pane),
		onOutput:     ctx.Sess.paneOutputCallback(),
		onExit:       ctx.Sess.paneExitCallback(),
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		pane := mctx.findPaneByID(target.pane.ID)
		if pane == nil {
			mctx.ScheduleClose(newPane)
			return commandMutationResult{err: fmt.Errorf("pane %q not found", target.pane.Meta.Name)}
		}
		if pane != target.pane {
			mctx.ScheduleClose(newPane)
			return commandMutationResult{err: fmt.Errorf("pane %q changed during respawn", target.pane.Meta.Name)}
		}
		w := mctx.findWindowByPaneID(pane.ID)
		if w == nil {
			mctx.ScheduleClose(newPane)
			return commandMutationResult{err: fmt.Errorf("pane not in any window")}
		}
		if err := mctx.replacePaneInstance(pane, newPane, w); err != nil {
			mctx.ScheduleClose(newPane)
			return commandMutationResult{err: err}
		}

		pane.SuppressCallbacks()
		mctx.ScheduleStart(newPane)
		mctx.ScheduleClose(pane)
		return clearedPaneRenderResult(
			fmt.Sprintf("Respawned %s\n", newPane.Meta.Name),
			newPane,
			true,
			nil,
			nil,
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

func cmdReset(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Reset(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runKill(ctx *CommandContext, actorPaneID uint32, opts killCommandArgs) commandpkg.Result {
	target, err := ctx.Sess.queryKillTarget(actorPaneID, opts.paneRef)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if target.paneID == 0 {
		return commandpkg.Result{}
	}

	if target.proxy && ctx.Sess.RemoteManager != nil {
		if err := ctx.Sess.RemoteManager.KillPane(target.paneID, opts.cleanup, opts.timeout); err != nil {
			return commandpkg.Result{Err: err}
		}
		verb := "Killed"
		if opts.cleanup {
			verb = "Cleaning up"
		}
		return commandpkg.Result{Output: fmt.Sprintf("%s %s\n", verb, target.paneName)}
	}

	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		pane := mctx.findPaneByID(target.paneID)
		if pane == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", target.paneName)}
		}
		if opts.cleanup {
			if err := mctx.beginPaneCleanupKill(pane, opts.timeout); err != nil {
				return commandMutationResult{err: err}
			}
			return commandMutationResult{
				output: fmt.Sprintf("Cleaning up %s\n", pane.Meta.Name),
			}
		}

		removed := mctx.softClosePane(pane.ID)
		if removed.pane == nil {
			return commandMutationResult{}
		}

		mctx.appendPaneLog(paneLogEventExit, removed.pane, "killed")
		mctx.emitEvent(Event{
			Type:     EventPaneExit,
			PaneID:   pane.ID,
			PaneName: removed.paneName,
			Host:     removed.pane.Meta.Host,
			Reason:   "killed",
		})

		if removed.sendExit {
			mctx.ScheduleClose(removed.pane)
			return commandMutationResult{
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

func cmdKill(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Kill(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runUndo(ctx *CommandContext) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		pane, err := mctx.undoClosePane()
		if err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Restored %s\n", pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdUndo(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Undo(layoutCommandContext{ctx}, ctx.Args))
}

type copyModeOptions struct {
	paneRef           string
	waitCopyModeShown bool
	waitTimeout       time.Duration
}

func parseCopyModeArgs(args []string) (copyModeOptions, error) {
	parsed, err := layoutcmd.ParseCopyModeArgs(args, defaultCommandUIWaitTimeout)
	if err != nil {
		return copyModeOptions{}, err
	}
	return copyModeOptions{
		paneRef:           parsed.PaneRef,
		waitCopyModeShown: parsed.WaitCopyModeShown,
		waitTimeout:       parsed.WaitTimeout,
	}, nil
}

func runCopyMode(ctx *CommandContext, actorPaneID uint32, opts copyModeOptions) commandpkg.Result {
	var err error
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
		pane, err = ctx.Sess.queryResolvedPaneForActor(actorPaneID, opts.paneRef)
	}
	if err != nil {
		return commandpkg.Result{Err: err}
	}

	var uiWait uiClientSnapshot
	if opts.waitCopyModeShown {
		uiWait, err = ctx.Sess.queryUIClient("", proto.UIEventCopyModeShown)
		if err != nil {
			return commandpkg.Result{Err: err}
		}
	}

	ctx.Sess.broadcast(&Message{Type: MsgTypeCopyMode, PaneID: pane.paneID})
	if opts.waitCopyModeShown {
		if err := waitForNextUIEvent(ctx.Sess, uiWait, proto.UIEventCopyModeShown, opts.waitTimeout); err != nil {
			return commandpkg.Result{Err: err}
		}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Copy mode entered for %s\n", pane.paneName)}
}

func cmdCopyMode(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.CopyMode(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runNewWindow(ctx *CommandContext, name string) commandpkg.Result {
	activePid, _, _, err := ctx.activeWindowSnapshot()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	meta := mux.PaneMeta{Dir: mux.PaneCwd(activePid)}
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		prepared, err := mctx.preparePendingLocalPane(ctx.Srv, meta, w.Width, mux.PaneContentHeight(w.Height), "")
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane := prepared.pane

		winID := mctx.nextWindowID()
		newWin := mux.NewWindow(pane, w.Width, w.Height)
		newWin.ID = winID
		newWin.LeadPaneID = pane.ID
		if name != "" {
			newWin.Name = name
		} else {
			newWin.Name = fmt.Sprintf(WindowNameFormat, winID)
		}
		mctx.Windows = append(mctx.Windows, newWin)
		mctx.activateWindow(newWin)
		mctx.startPendingLocalPaneBuild(ctx.Srv, pane, prepared.build)

		return commandMutationResult{
			output:          fmt.Sprintf("Created %s\n", newWin.Name),
			broadcastLayout: true,
		}
	}))
}

func runSelectWindow(ctx *CommandContext, ref string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.resolveWindow(ref)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("window %q not found", ref)}
		}
		mctx.activateWindow(w)
		return commandMutationResult{
			output:          "Switched window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(w),
		}
	}))
}

func runNextWindow(ctx *CommandContext) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		mctx.nextWindow()
		return commandMutationResult{
			output:          "Next window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(mctx.activeWindow()),
		}
	}))
}

func runPrevWindow(ctx *CommandContext) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		mctx.prevWindow()
		return commandMutationResult{
			output:          "Previous window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(mctx.activeWindow()),
		}
	}))
}

func runRenameWindow(ctx *CommandContext, name string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
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

func runResizeBorder(ctx *CommandContext, x, y, delta int) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if w := mctx.activeWindow(); w != nil {
			w.ResizeBorder(x, y, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func runResizeActive(ctx *CommandContext, direction string, delta int) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if w := mctx.activeWindow(); w != nil {
			if w.ActivePane != nil && w.IsLeadPane(w.ActivePane.ID) {
				return commandMutationResult{err: fmt.Errorf("cannot operate on lead pane")}
			}
			w.ResizeActive(direction, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func runResizePane(ctx *CommandContext, actorPaneID uint32, paneRef, direction string, delta int) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		p, w, err := mctx.resolvePaneWindowForActor(actorPaneID, "resize-pane", []string{paneRef})
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
	return layoutcmd.ParseEqualizeCommandArgs(args)
}

func runEqualize(ctx *CommandContext, widths, heights bool) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.activeWindow()
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

func cmdEqualize(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.Equalize(layoutCommandContext{ctx}, ctx.Args))
}

func runResizeWindow(ctx *CommandContext, cols, rows int) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		layoutH := rows - render.GlobalBarHeight
		for _, w := range mctx.Windows {
			w.Resize(cols, layoutH)
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Resized to %dx%d\n", cols, rows),
			broadcastLayout: true,
		}
	}))
}

func cmdResizeWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizeWindow(layoutCommandContext{ctx}, ctx.Args))
}

func runSetLead(ctx *CommandContext, actorPaneID uint32, paneRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		pane := w.ActivePane
		if paneRef != "" {
			resolved, err := w.ResolvePane(paneRef)
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

func cmdSetLead(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.SetLead(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runUnsetLead(ctx *CommandContext, actorPaneID uint32) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.windowForActor(actorPaneID)
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

func cmdUnsetLead(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.UnsetLead(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runToggleLead(ctx *CommandContext, actorPaneID uint32) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		w := mctx.windowForActor(actorPaneID)
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

func cmdToggleLead(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ToggleLead(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}
