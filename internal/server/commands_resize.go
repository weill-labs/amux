package server

import layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"

func cmdResizeBorder(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizeBorder(layoutCommandContext{ctx}, ctx.Args))
}

func cmdResizeActive(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizeActive(layoutCommandContext{ctx}, ctx.Args))
}

func cmdResizePane(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizePane(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}
