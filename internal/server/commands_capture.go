package server

import (
	"os"

	caputil "github.com/weill-labs/amux/internal/capture"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
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

func captureLegacyClientPathEnabled() bool {
	return os.Getenv("AMUX_CAPTURE_LEGACY_CLIENT") == "1"
}

func captureLocally(ctx *CommandContext, args []string) *Message {
	req := caputil.ParseArgs(args)
	if req.PaneRef == "" {
		return captureFullSessionLocally(ctx, args)
	}
	return captureSinglePaneLocally(ctx, req)
}

func captureSinglePaneLocally(ctx *CommandContext, req caputil.Request) *Message {
	if err := caputil.ValidateScreenRequest(req); err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	if req.ColorMap {
		return &Message{Type: MsgTypeCmdResult, CmdErr: "--colors is only supported for full screen capture"}
	}

	target, err := ctx.Sess.resolveCapturePaneTargetForActor(ctx.ActorPaneID, req.PaneRef)
	if err != nil {
		return &Message{Type: MsgTypeCmdResult, CmdErr: err.Error()}
	}
	return ctx.Sess.capturePaneDirect(caputil.ArgsForRequest(req), target)
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
	if !req.ClientMode && !req.DisplayMode && !captureLegacyClientPathEnabled() {
		if req.PaneRef == "" && (!req.HistoryMode || req.FormatJSON) {
			ctx.applyCommandResult(commandpkg.Result{Message: captureLocally(ctx, ctx.Args)})
			return
		}
		if req.PaneRef == "" {
			ctx.applyCommandResult(capturecmd.Capture(captureCommandContext{ctx}, ctx.Args))
			return
		}
		if req.HistoryMode {
			ctx.applyCommandResult(capturecmd.Capture(captureCommandContext{ctx}, ctx.Args))
			return
		}
		ctx.applyCommandResult(commandpkg.Result{Message: captureLocally(ctx, ctx.Args)})
		return
	}
	ctx.applyCommandResult(capturecmd.Capture(captureCommandContext{ctx}, ctx.Args))
}
