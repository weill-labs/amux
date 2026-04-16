package wait

import (
	"errors"
	"fmt"
	"time"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

const (
	waitCommandUsage   = "usage: wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ..."
	cursorCommandUsage = "usage: cursor <layout|clipboard|ui> [--client <id>]"
)

type CheckpointRecord struct {
	Generation uint64
	Path       string
}

type Context interface {
	Generation() uint64
	LayoutJSON() (string, error)
	WaitLayout(afterGen uint64, afterSet bool, timeout time.Duration) (gen uint64, ok bool)
	ClipboardGeneration() uint64
	WaitClipboard(afterGen uint64, afterSet bool, timeout time.Duration) (data string, ok bool)
	WaitCheckpoint(afterGen uint64, afterSet bool, timeout time.Duration) (CheckpointRecord, bool)
	UIGeneration(requestedClientID string) (uint64, error)
	WaitContent(actorPaneID uint32, paneRef, substr string, timeout time.Duration) error
	WaitExited(actorPaneID uint32, paneRef string, timeout time.Duration) error
	WaitBusy(actorPaneID uint32, paneRef string, timeout time.Duration) error
	WaitUI(eventName, requestedClientID string, afterGen uint64, afterSet bool, timeout time.Duration) error
	WaitReady(actorPaneID uint32, args []string) error
	WaitIdle(actorPaneID uint32, args []string) error
}

func Cursor(ctx Context, args []string) commandpkg.Result {
	if len(args) == 0 {
		return commandpkg.Result{Err: errors.New(cursorCommandUsage)}
	}

	switch args[0] {
	case "layout":
		return Generation(ctx, args[1:])
	case "clipboard":
		return ClipboardGen(ctx, args[1:])
	case "ui":
		return UIGen(ctx, args[1:])
	default:
		return commandpkg.Result{Err: fmt.Errorf("unknown cursor kind: %s", args[0])}
	}
}

func Wait(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) == 0 {
		return commandpkg.Result{Err: errors.New(waitCommandUsage)}
	}

	switch args[0] {
	case "layout":
		return WaitLayout(ctx, args[1:])
	case "clipboard":
		return WaitClipboard(ctx, args[1:])
	case "checkpoint":
		return WaitCheckpoint(ctx, args[1:])
	case "content":
		return WaitFor(ctx, actorPaneID, args[1:])
	case "ready":
		return WaitReady(ctx, actorPaneID, args[1:])
	case "idle":
		return WaitIdle(ctx, actorPaneID, args[1:])
	case "exited":
		return WaitExited(ctx, actorPaneID, args[1:])
	case "busy":
		return WaitBusy(ctx, actorPaneID, args[1:])
	case "ui":
		return WaitUI(ctx, args[1:])
	default:
		return commandpkg.Result{Err: fmt.Errorf("unknown wait kind: %s", args[0])}
	}
}

func Generation(ctx Context, _ []string) commandpkg.Result {
	return commandpkg.Result{Output: fmt.Sprintf("%d\n", ctx.Generation())}
}

func LayoutJSON(ctx Context, _ []string) commandpkg.Result {
	data, err := ctx.LayoutJSON()
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: data}
}

func WaitLayout(ctx Context, args []string) commandpkg.Result {
	afterGen, afterSet, timeout, err := ParseWaitArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	gen, ok := ctx.WaitLayout(afterGen, afterSet, timeout)
	if !ok {
		return commandpkg.Result{Err: fmt.Errorf("timeout waiting for generation > %d (current: %d)", afterGen, gen)}
	}
	return commandpkg.Result{Output: fmt.Sprintf("%d\n", gen)}
}

func ClipboardGen(ctx Context, _ []string) commandpkg.Result {
	return commandpkg.Result{Output: fmt.Sprintf("%d\n", ctx.ClipboardGeneration())}
}

func WaitClipboard(ctx Context, args []string) commandpkg.Result {
	afterGen, afterSet, timeout, err := ParseWaitArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	data, ok := ctx.WaitClipboard(afterGen, afterSet, timeout)
	if !ok {
		return commandpkg.Result{Err: fmt.Errorf("timeout waiting for clipboard event")}
	}
	return commandpkg.Result{Output: data + "\n"}
}

func WaitCheckpoint(ctx Context, args []string) commandpkg.Result {
	afterGen, afterSet, timeout, err := ParseWaitArgsWithDefault(args, 15*time.Second)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	record, ok := ctx.WaitCheckpoint(afterGen, afterSet, timeout)
	if !ok {
		return commandpkg.Result{Err: fmt.Errorf("timeout waiting for checkpoint write after %d", afterGen)}
	}
	return commandpkg.Result{Output: fmt.Sprintf("%d %s\n", record.Generation, record.Path)}
}

func UIGen(ctx Context, args []string) commandpkg.Result {
	requestedClientID, err := ParseUIGenArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	gen, err := ctx.UIGeneration(requestedClientID)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("%d\n", gen)}
}

func WaitFor(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) < 2 {
		return commandpkg.Result{Err: fmt.Errorf("usage: wait content <pane> <substring> [--timeout <duration>]")}
	}
	paneRef := args[0]
	substr := args[1]
	timeout, err := ParseTimeout(args, 2, 10*time.Second)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := ctx.WaitContent(actorPaneID, paneRef, substr, timeout); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: "matched\n"}
}

func WaitReady(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if err := ctx.WaitReady(actorPaneID, args); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: "ready\n"}
}

func WaitIdle(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if err := ctx.WaitIdle(actorPaneID, args); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: "idle\n"}
}

func WaitExited(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: wait exited <pane> [--timeout <duration>]")}
	}
	paneRef := args[0]
	timeout, err := ParseTimeout(args, 1, 5*time.Second)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := ctx.WaitExited(actorPaneID, paneRef, timeout); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: "exited\n"}
}

func WaitBusy(ctx Context, actorPaneID uint32, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: wait busy <pane> [--timeout <duration>]")}
	}
	paneRef := args[0]
	timeout, err := ParseTimeout(args, 1, 5*time.Second)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := ctx.WaitBusy(actorPaneID, paneRef, timeout); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: "busy\n"}
}

func WaitUI(ctx Context, args []string) commandpkg.Result {
	eventName, requestedClientID, afterGen, afterSet, timeout, err := ParseWaitUIArgs(args)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := ctx.WaitUI(eventName, requestedClientID, afterGen, afterSet, timeout); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: eventName + "\n"}
}
