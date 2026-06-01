package server

func cmdDebugFrames(ctx *CommandContext) {
	if len(ctx.Args) != 0 {
		ctx.replyErr("debug-frames does not accept arguments")
		return
	}
	if err := ctx.CC.Send(ctx.Sess.forwardDebugFramesForActor(ctx.ActorPaneID)); err != nil {
		return
	}
}

func cmdDebugScrollback(ctx *CommandContext) {
	if len(ctx.Args) != 0 {
		ctx.replyErr("debug-scrollback does not accept arguments")
		return
	}
	stats, err := ctx.Sess.queryScrollbackDebugStatsContext(ctx.context())
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(formatDebugScrollback(stats))
}
