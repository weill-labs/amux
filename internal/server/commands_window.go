package server

import (
	"fmt"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"
)

func cmdNewWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.NewWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdSelectWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.SelectWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdNextWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.NextWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdPrevWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.PrevWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdLastWindow(ctx *CommandContext) {
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(ctx *MutationContext) commandMutationResult {
		if !ctx.lastWindow() {
			return commandMutationResult{bell: true}
		}
		return commandMutationResult{
			output:          "Last window\n",
			broadcastLayout: true,
			paneRenders:     activePaneRender(ctx.activeWindow()),
		}
	}))
}

func cmdRenameWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.RenameWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdReorderWindow(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ReorderWindow(layoutCommandContext{ctx}, ctx.Args))
}

func cmdMovePaneToWindow(ctx *CommandContext) {
	if len(ctx.Args) != 2 {
		ctx.applyCommandResult(commandpkg.Result{Err: fmt.Errorf("usage: move-pane-to-window <pane> <window>")})
		return
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if err := movePaneToWindow(mctx, ctx.ActorPaneID, ctx.Args[0], ctx.Args[1]); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func movePaneToWindow(mctx *MutationContext, actorPaneID uint32, paneRef, windowRef string) error {
	pane, _, err := mctx.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
	if err != nil {
		return err
	}
	target := mctx.resolveWindow(windowRef)
	if target == nil {
		return fmt.Errorf("window %q not found", windowRef)
	}
	return mutationContextDo(mctx, func(sess *Session) error {
		return sess.movePaneToWindow(pane.ID, target.ID)
	})
}
