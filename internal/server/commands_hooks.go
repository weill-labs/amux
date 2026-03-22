package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/weill-labs/amux/internal/hooks"
)

func cmdSetHook(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: set-hook <event> <command>")
		return
	}
	event, err := hooks.ParseEvent(ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	command := strings.Join(ctx.Args[1:], " ")
	ctx.Sess.Hooks.Add(event, command)
	ctx.reply(fmt.Sprintf("Hook added: %s → %s\n", event, command))
}

func cmdUnsetHook(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: unset-hook <event> [index]")
		return
	}
	event, err := hooks.ParseEvent(ctx.Args[0])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if len(ctx.Args) >= 2 {
		idx, err := strconv.Atoi(ctx.Args[1])
		if err != nil {
			ctx.replyErr(fmt.Sprintf("invalid index: %s", ctx.Args[1]))
			return
		}
		ctx.Sess.Hooks.Remove(event, idx)
		ctx.reply(fmt.Sprintf("Removed hook %d for %s\n", idx, event))
	} else {
		ctx.Sess.Hooks.RemoveAll(event)
		ctx.reply(fmt.Sprintf("Removed all hooks for %s\n", event))
	}
}

func cmdListHooks(ctx *CommandContext) {
	var output strings.Builder
	hasAny := false
	for _, event := range hooks.AllEvents {
		entries := ctx.Sess.Hooks.List(event)
		if len(entries) == 0 {
			continue
		}
		hasAny = true
		output.WriteString(fmt.Sprintf("%s:\n", event))
		for i, entry := range entries {
			output.WriteString(fmt.Sprintf("  %d: %s\n", i, entry.Command))
		}
	}
	if !hasAny {
		ctx.reply("No hooks registered.\n")
		return
	}
	ctx.reply(output.String())
}

func cmdEvents(ctx *CommandContext) {
	ea := parseEventsArgs(ctx.Args)
	res := ctx.Sess.enqueueEventSubscribe(ea.filter, true)
	if res.sub == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	for _, data := range res.initialState {
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
			return
		}
	}

	if ea.throttle <= 0 {
		for data := range res.sub.ch {
			if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
				return
			}
		}
		return
	}

	ticker := time.NewTicker(ea.throttle)
	defer ticker.Stop()
	pending := make(map[uint32][]byte)

	for {
		select {
		case data, ok := <-res.sub.ch:
			if !ok {
				return
			}
			if paneID, isOutput := peekOutputPaneID(data); isOutput {
				pending[paneID] = data
			} else {
				if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(data) + "\n"}); err != nil {
					return
				}
			}
		case <-ticker.C:
			if err := flushPendingOutputEvents(ctx, pending); err != nil {
				return
			}
		}
	}
}

func flushPendingOutputEvents(ctx *CommandContext, pending map[uint32][]byte) error {
	if len(pending) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: string(pending[id]) + "\n"}); err != nil {
			return err
		}
		delete(pending, id)
	}
	return nil
}

func peekOutputPaneID(data []byte) (uint32, bool) {
	if !bytes.Contains(data, []byte(`"type":"output"`)) {
		return 0, false
	}
	var partial struct {
		PaneID uint32 `json:"pane_id"`
	}
	if err := json.Unmarshal(data, &partial); err != nil {
		return 0, false
	}
	return partial.PaneID, true
}
