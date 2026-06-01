package input

import (
	"fmt"
	"strings"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Context interface {
	SendKeys(actorPaneID uint32, args []string) (paneName string, byteCount int, submitted bool, err error)
	Broadcast(actorPaneID uint32, args []string) (paneNames []string, byteCount int, err error)
	TypeKeys(args []string) (byteCount int, err error)
}

func SendKeys(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneName, byteCount, submitted, err := ctx.SendKeys(actorPaneID, args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	verb := "Sent"
	if submitted {
		verb = "Submitted"
	}
	return commandpkg.Result{Output: fmt.Sprintf("%s %d bytes to %s\n", verb, byteCount, paneName)}
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
