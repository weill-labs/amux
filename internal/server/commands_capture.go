package server

import caputil "github.com/weill-labs/amux/internal/capture"

func cmdCapture(ctx *CommandContext) {
	req := caputil.ParseArgs(ctx.Args)
	if req.HistoryMode {
		ctx.CC.Send(ctx.Sess.captureHistory(ctx.Args))
		return
	}
	if req.PaneRef != "" {
		ctx.CC.Send(ctx.Sess.capturePaneWithFallback(ctx.Args))
		return
	}
	result := ctx.Sess.forwardCapture(ctx.Args)
	ctx.CC.Send(result)
}
