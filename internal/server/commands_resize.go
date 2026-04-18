package server

import layoutcmd "github.com/weill-labs/amux/internal/server/commands/layout"

func cmdResizeBorder(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizeBorder(layoutCommandContext{ctx}, ctx.Args))
}

func cmdResizeActive(ctx *CommandContext) {
	ctx.applyCommandResult(layoutcmd.ResizeActive(layoutCommandContext{ctx}, ctx.Args))
}

func cmdResizePane(ctx *CommandContext) {
	if len(ctx.Args) > 0 {
		ref, err := ctx.Sess.queryPaneRef(ctx.Args[0])
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if ref.Host != "" {
			ctx.applyCommandResult(remoteCommandResult(ctx.Sess, ref.Host, "resize-pane", rewritePaneRefArg(ctx.Args, 0, ref.Pane)))
			return
		}
	}
	ctx.applyCommandResult(layoutcmd.ResizePane(layoutCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}
