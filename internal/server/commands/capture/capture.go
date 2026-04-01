package capture

import (
	caputil "github.com/weill-labs/amux/internal/capture"
	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Context interface {
	CaptureHistory(args []string) *proto.Message
	CapturePaneWithFallback(args []string) *proto.Message
	ForwardCapture(args []string) *proto.Message
}

func Capture(ctx Context, args []string) commandpkg.Result {
	req := caputil.ParseArgs(args)
	switch {
	case req.HistoryMode && (req.PaneRef != "" || !req.FormatJSON):
		return commandpkg.Result{Message: ctx.CaptureHistory(args)}
	case req.PaneRef != "":
		return commandpkg.Result{Message: ctx.CapturePaneWithFallback(args)}
	default:
		return commandpkg.Result{Message: ctx.ForwardCapture(args)}
	}
}
