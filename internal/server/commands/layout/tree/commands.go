package tree

import (
	"fmt"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Context interface {
	SwapForward(actorPaneID uint32) commandpkg.Result
	SwapBackward(actorPaneID uint32) commandpkg.Result
	Swap(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result
	SwapTree(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result
	Move(actorPaneID uint32, paneRef, targetRef string, before bool) commandpkg.Result
	MoveTo(actorPaneID uint32, paneRef, targetRef string) commandpkg.Result
	MoveSibling(actorPaneID uint32, paneRef, direction string) commandpkg.Result
	Rotate(forward bool) commandpkg.Result
}

func Swap(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	switch {
	case len(args) == 1 && args[0] == "forward":
		return ctx.SwapForward(actorPaneID)
	case len(args) == 1 && args[0] == "backward":
		return ctx.SwapBackward(actorPaneID)
	case len(args) == 2:
		return ctx.Swap(actorPaneID, args[0], args[1])
	default:
		return commandpkg.Result{Err: fmt.Errorf("usage: swap <pane1> <pane2> | swap forward | swap backward")}
	}
}

func SwapTree(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneRef, targetRef, err := ParseMoveToArgs(args)
	if err != nil {
		return commandpkg.Result{Err: fmt.Errorf("usage: swap-tree <pane1> <pane2>")}
	}
	return ctx.SwapTree(actorPaneID, paneRef, targetRef)
}

func Move(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneRef, targetRef, before, err := ParseMoveArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.Move(actorPaneID, paneRef, targetRef, before)
}

func MoveTo(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	paneRef, targetRef, err := ParseMoveToArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.MoveTo(actorPaneID, paneRef, targetRef)
}

func MoveUp(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	return moveSibling(ctx, actorPaneID, args, MoveUpUsage, "up")
}

func MoveDown(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	return moveSibling(ctx, actorPaneID, args, MoveDownUsage, "down")
}

func Rotate(ctx Context, args []string) commandpkg.Result {
	forward := true
	for _, arg := range args {
		if arg == "--reverse" {
			forward = false
		}
	}
	return ctx.Rotate(forward)
}

func moveSibling(ctx Context, actorPaneID uint32, args []string, usage, direction string) commandpkg.Result {
	paneRef, err := ParseMoveSiblingArgs(args, usage)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return ctx.MoveSibling(actorPaneID, paneRef, direction)
}
