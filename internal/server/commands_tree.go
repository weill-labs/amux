package server

import (
	"fmt"

	treecmd "github.com/weill-labs/amux/internal/server/commands/layout/tree"
)

const moveUsage = treecmd.MoveUsage

func parseMoveArgs(args []string) (paneRef, targetRef string, before bool, err error) {
	return treecmd.ParseMoveArgs(args)
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

func cmdSwapTree(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if len(ctx.Args) != 2 {
			return commandMutationResult{err: fmt.Errorf("usage: swap-tree <pane1> <pane2>")}
		}

		w := sess.windowForActor(ctx.ActorPaneID)
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no session")}
		}

		pane1 := w.ResolvePane(ctx.Args[0])
		if pane1 == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", ctx.Args[0])}
		}
		pane2 := w.ResolvePane(ctx.Args[1])
		if pane2 == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", ctx.Args[1])}
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

		pane := w.ResolvePane(paneRef)
		if pane == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", paneRef)}
		}
		target := w.ResolvePane(targetRef)
		if target == nil {
			return commandMutationResult{err: fmt.Errorf("pane %q not found", targetRef)}
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

		w.RotatePanes(forward)
		return commandMutationResult{output: "Rotated\n", broadcastLayout: true}
	}))
}
