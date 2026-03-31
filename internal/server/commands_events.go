package server

import (
	"bytes"
	"encoding/json"
	"slices"
	"time"
)

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
		for data := range res.sub.Ch {
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
		case data, ok := <-res.sub.Ch:
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
