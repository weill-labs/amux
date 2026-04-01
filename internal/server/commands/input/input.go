package input

import (
	"fmt"
	"strings"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Context interface {
	SendKeys(actorPaneID uint32, args []string) (paneName string, byteCount int, err error)
	Broadcast(actorPaneID uint32, args []string) (paneNames []string, byteCount int, err error)
	TypeKeys(args []string) (byteCount int, err error)
}

func SendKeys(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneName, byteCount, err := ctx.SendKeys(actorPaneID, args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Sent %d bytes to %s\n", byteCount, paneName)}
}

func Broadcast(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneNames, byteCount, err := ctx.Broadcast(actorPaneID, args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}

	noun := "panes"
	if len(paneNames) == 1 {
		noun = "pane"
	}
	return commandpkg.Result{
		Output: fmt.Sprintf("Sent %d bytes to %d %s: %s\n", byteCount, len(paneNames), noun, strings.Join(paneNames, ", ")),
	}
}

func TypeKeys(ctx Context, args []string) commandpkg.Result {
	byteCount, err := ctx.TypeKeys(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Typed %d bytes\n", byteCount)}
}
