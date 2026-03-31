package server

import capturecmd "github.com/weill-labs/amux/internal/server/commands/capture"

type captureCommandContext struct {
	*CommandContext
}

func (ctx captureCommandContext) CaptureHistory(args []string) *Message {
	return ctx.Sess.captureHistory(ctx.ActorPaneID, args)
}

func (ctx captureCommandContext) CapturePaneWithFallback(args []string) *Message {
	return ctx.Sess.capturePaneWithFallback(ctx.ActorPaneID, args)
}

func (ctx captureCommandContext) ForwardCapture(args []string) *Message {
	return ctx.Sess.forwardCaptureForActor(ctx.ActorPaneID, args)
}

func cmdCapture(ctx *CommandContext) {
	ctx.applyCommandResult(capturecmd.Capture(captureCommandContext{ctx}, ctx.Args))
}
