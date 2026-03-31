package server

import (
	"fmt"
	"strconv"
)

func cmdResizeBorder(ctx *CommandContext) {
	if len(ctx.Args) < 3 {
		ctx.replyErr("usage: resize-border <x> <y> <delta>")
		return
	}
	x, err1 := strconv.Atoi(ctx.Args[0])
	y, err2 := strconv.Atoi(ctx.Args[1])
	delta, err3 := strconv.Atoi(ctx.Args[2])
	if err1 != nil || err2 != nil || err3 != nil {
		ctx.replyErr("resize-border: invalid arguments")
		return
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if w := sess.activeWindow(); w != nil {
			w.ResizeBorder(x, y, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdResizeActive(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: resize-active <direction> <delta>")
		return
	}
	direction := ctx.Args[0]
	delta, err := strconv.Atoi(ctx.Args[1])
	if err != nil {
		ctx.replyErr("resize-active: invalid delta")
		return
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		if w := sess.activeWindow(); w != nil {
			if w.ActivePane != nil && w.IsLeadPane(w.ActivePane.ID) {
				return commandMutationResult{err: fmt.Errorf("cannot operate on lead pane")}
			}
			w.ResizeActive(direction, delta)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdResizePane(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: resize-pane <pane> <direction> [delta]")
		return
	}
	direction := ctx.Args[1]
	switch direction {
	case "left", "right", "up", "down":
	default:
		ctx.replyErr(fmt.Sprintf("resize-pane: invalid direction %q (use left/right/up/down)", direction))
		return
	}
	delta := 1
	if len(ctx.Args) >= 3 {
		var err error
		delta, err = strconv.Atoi(ctx.Args[2])
		if err != nil || delta <= 0 {
			ctx.replyErr("resize-pane: invalid delta")
			return
		}
	}
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		p, w, err := sess.resolvePaneWindowForActor(ctx.ActorPaneID, "resize-pane", ctx.Args[:1])
		if err != nil {
			return commandMutationResult{err: err}
		}
		if w.IsLeadPane(p.ID) {
			return commandMutationResult{err: fmt.Errorf("cannot operate on lead pane")}
		}
		w.ResizePane(p.ID, direction, delta)
		return commandMutationResult{
			output:          fmt.Sprintf("Resized %s %s by %d\n", p.Meta.Name, direction, delta),
			broadcastLayout: true,
		}
	}))
}
