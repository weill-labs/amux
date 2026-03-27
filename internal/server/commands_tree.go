package server

import (
	"fmt"

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

func cmdSwap(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.windowForActor(ctx.ActorPaneID)
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
			pane1, err := w.ResolvePane(ctx.Args[0])
			if err != nil {
				return commandMutationResult{err: err}
			}
			pane2, err := w.ResolvePane(ctx.Args[1])
			if err != nil {
				return commandMutationResult{err: err}
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

func cmdSwapTree(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if len(ctx.Args) != 2 {
			return commandMutationResult{err: fmt.Errorf("usage: swap-tree <pane1> <pane2>")}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane1, err := w.ResolvePane(ctx.Args[0])
		if err != nil {
			return commandMutationResult{err: err}
		}
		pane2, err := w.ResolvePane(ctx.Args[1])
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.SwapTree(pane1.ID, pane2.ID); err != nil {
			return commandMutationResult{err: err}
		}

		return commandMutationResult{output: "Swapped tree\n", broadcastLayout: true}
	}))
}

func cmdMove(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		paneRef, targetRef, before, err := parseMoveArgs(ctx.Args)
		if err != nil {
			return commandMutationResult{err: err}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
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

func cmdMoveTo(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		paneRef, targetRef, err := parseMoveToArgs(ctx.Args)
		if err != nil {
			return commandMutationResult{err: err}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
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

func cmdMoveUp(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		paneRef, err := parseMoveSiblingArgs(ctx.Args, moveUpUsage)
		if err != nil {
			return commandMutationResult{err: err}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.MovePaneUp(pane.ID); err != nil {
			return commandMutationResult{err: err}
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Moved %s up\n", pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdMoveDown(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		paneRef, err := parseMoveSiblingArgs(ctx.Args, moveDownUsage)
		if err != nil {
			return commandMutationResult{err: err}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane, err := w.ResolvePane(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.MovePaneDown(pane.ID); err != nil {
			return commandMutationResult{err: err}
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Moved %s down\n", pane.Meta.Name),
			broadcastLayout: true,
		}
	}))
}

func cmdRotate(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		forward := true
		for _, arg := range ctx.Args {
			if arg == "--reverse" {
				forward = false
			}
		}

		if err := w.RotatePanes(forward); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{output: "Rotated\n", broadcastLayout: true}
	}))
}
