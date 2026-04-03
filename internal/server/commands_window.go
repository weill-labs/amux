package server

import layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"

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
