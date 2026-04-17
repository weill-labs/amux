package server

import (
	caputil "github.com/weill-labs/amux/internal/capture"
	capturecmd "github.com/weill-labs/amux/internal/server/commands/capture"
)

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
	req := caputil.ParseArgs(ctx.Args)
	if req.PaneRef != "" {
		ref, err := ctx.Sess.queryPaneRef(req.PaneRef)
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if ref.Host != "" {
			req.PaneRef = ref.Pane
			ctx.applyCommandResult(remoteCommandResult(ctx.Sess, ref.Host, "capture", caputil.ArgsForRequest(req)))
			return
		}
	}
	ctx.applyCommandResult(capturecmd.Capture(captureCommandContext{ctx}, ctx.Args))
}
