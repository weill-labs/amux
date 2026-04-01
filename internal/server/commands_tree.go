package server

import (
	"fmt"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	treecmd "github.com/weill-labs/amux/internal/server/commands/layout/tree"
)

const moveUsage = treecmd.MoveUsage
const moveToUsage = treecmd.MoveToUsage
const moveUpUsage = treecmd.MoveUpUsage
const moveDownUsage = treecmd.MoveDownUsage

func parseMoveArgs(args []string) (paneRef, targetRef string, before bool, err error) {
	return treecmd.ParseMoveArgs(args)
}

func parseMoveToArgs(args []string) (paneRef, targetRef string, err error) {
	return treecmd.ParseMoveToArgs(args)
}

func parseMoveSiblingArgs(args []string, usage string) (paneRef string, err error) {
	return treecmd.ParseMoveSiblingArgs(args, usage)
}

type treeCommandContext struct {
	*CommandContext
}

func (ctx treeCommandContext) SwapForward(actorPaneID uint32) commandpkg.Result {
	return runSwapForward(ctx.CommandContext, actorPaneID)
}

func (ctx treeCommandContext) SwapBackward(actorPaneID uint32) commandpkg.Result {
	return runSwapBackward(ctx.CommandContext, actorPaneID)
}

func (ctx treeCommandContext) Swap(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return runSwap(ctx.CommandContext, actorPaneID, paneRef, targetRef)
}

func (ctx treeCommandContext) SwapTree(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return runSwapTree(ctx.CommandContext, actorPaneID, paneRef, targetRef)
}

func (ctx treeCommandContext) Move(actorPaneID uint32, paneRef, targetRef string, before bool) commandpkg.Result {
	return runMove(ctx.CommandContext, actorPaneID, paneRef, targetRef, before)
}

func (ctx treeCommandContext) MoveTo(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return runMoveTo(ctx.CommandContext, actorPaneID, paneRef, targetRef)
}

func (ctx treeCommandContext) MoveSibling(actorPaneID uint32, paneRef, direction string) commandpkg.Result {
	return runMoveSibling(ctx.CommandContext, actorPaneID, paneRef, direction)
}

func (ctx treeCommandContext) Rotate(forward bool) commandpkg.Result {
	return runRotate(ctx.CommandContext, forward)
}

func runSwapForward(ctx *CommandContext, actorPaneID uint32) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		if err := w.SwapPaneForward(); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Swapped\n", broadcastLayout: true}
	}))
}

func runSwapBackward(ctx *CommandContext, actorPaneID uint32) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		if err := w.SwapPaneBackward(); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Swapped\n", broadcastLayout: true}
	}))
}

func runSwap(ctx *CommandContext, actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane1, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane2, err := w.ResolvePane(targetRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.SwapPanes(pane1.ID, pane2.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Swapped\n", broadcastLayout: true}
	}))
}

func cmdSwap(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.Swap(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runSwapTree(ctx *CommandContext, actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane1, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane2, err := w.ResolvePane(targetRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.SwapTree(pane1.ID, pane2.ID); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Swapped tree\n", broadcastLayout: true}
	}))
}

func cmdSwapTree(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.SwapTree(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runMove(ctx *CommandContext, actorPaneID uint32, paneRef, targetRef string, before bool) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		target, err := w.ResolvePane(targetRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.MovePane(pane.ID, target.ID, before); err != nil {
			return commandMutationResult{err: err}
		}

		pos := "after"
		if before {
			pos = "before"
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Moved %s %s %s\n", pane.Meta.Name, pos, target.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdMove(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.Move(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runMoveTo(ctx *CommandContext, actorPaneID uint32, paneRef, targetRef string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		target, err := w.ResolvePane(targetRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.MovePaneToColumn(pane.ID, target.ID); err != nil {
			return commandMutationResult{err: err}
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Moved %s to %s's column\n", pane.Meta.Name, target.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdMoveTo(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.MoveTo(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runMoveSibling(ctx *CommandContext, actorPaneID uint32, paneRef, direction string) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(actorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		var moveErr error
		switch direction {
		case "up":
			moveErr = w.MovePaneUp(pane.ID)
		case "down":
			moveErr = w.MovePaneDown(pane.ID)
		default:
			moveErr = fmt.Errorf("unknown move direction: %s", direction)
		}
		if moveErr != nil {
			return commandMutationResult{err: moveErr}
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Moved %s %s\n", pane.Meta.Name, direction),
			broadcastLayout: true,
		}
	}))
}

func cmdMoveUp(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.MoveUp(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func cmdMoveDown(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.MoveDown(treeCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func runRotate(ctx *CommandContext, forward bool) commandpkg.Result {
	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}
		if err := w.RotatePanes(forward); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Rotated\n", broadcastLayout: true}
	}))
}

func cmdRotate(ctx *CommandContext) {
	ctx.applyCommandResult(treecmd.Rotate(treeCommandContext{ctx}, ctx.Args))
}
