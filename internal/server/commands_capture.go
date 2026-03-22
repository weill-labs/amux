package server

import caputil "github.com/weill-labs/amux/internal/capture"

func cmdCapture(ctx *CommandContext) {
	req := caputil.ParseArgs(ctx.Args)
	if req.HistoryMode {
		ctx.CC.Send(ctx.Sess.captureHistory(ctx.ActorPaneID, ctx.Args))
		return
	}
	if req.PaneRef != "" {
		ctx.CC.Send(ctx.Sess.capturePaneWithFallback(ctx.ActorPaneID, ctx.Args))
		return
	}
	result := ctx.Sess.forwardCaptureForActor(ctx.ActorPaneID, ctx.Args)
	ctx.CC.Send(result)
}
