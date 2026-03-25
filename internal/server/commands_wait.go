package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	waitcmd "github.com/weill-labs/amux/internal/server/commands/wait"
)

const (
	waitCommandUsage   = "usage: wait <idle|busy|vt-idle|ready|content|layout|clipboard|hook|ui> ..."
	cursorCommandUsage = "usage: cursor <layout|clipboard|hook|ui> [--client <id>]"
)

func waitSubcommandContext(ctx *CommandContext, args []string) *CommandContext {
	sub := *ctx
	sub.Args = args
	return &sub
}

func parseWaitArgs(args []string) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return waitcmd.ParseWaitArgs(args)
}

func parseTimeout(args []string, startIdx int, defaultTimeout time.Duration) (time.Duration, error) {
	return waitcmd.ParseTimeout(args, startIdx, defaultTimeout)
}

func parseUIGenArgs(args []string) (clientID string, err error) {
	return waitcmd.ParseUIGenArgs(args)
}

func parseWaitHookArgs(args []string) (eventName, paneName string, afterGen uint64, timeout time.Duration, err error) {
	return waitcmd.ParseWaitHookArgs(args)
}

func parseWaitUIArgs(args []string) (eventName, clientID string, afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return waitcmd.ParseWaitUIArgs(args)
}

func waitBusyForegroundPID(status mux.AgentStatus) int {
	return waitcmd.WaitBusyForegroundPID(status)
}

func waitBusyReady(candidatePID int, status mux.AgentStatus) (nextPID int, ready bool) {
	return waitcmd.WaitBusyReady(candidatePID, status)
}

func cmdCursor(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		ctx.replyErr(cursorCommandUsage)
		return
	}

	switch ctx.Args[0] {
	case "layout":
		cmdGeneration(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "clipboard":
		cmdClipboardGen(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "hook":
		cmdHookGen(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "ui":
		cmdUIGen(waitSubcommandContext(ctx, ctx.Args[1:]))
	default:
		ctx.replyErr(fmt.Sprintf("unknown cursor kind: %s", ctx.Args[0]))
	}
}

func cmdWait(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		ctx.replyErr(waitCommandUsage)
		return
	}

	switch ctx.Args[0] {
	case "layout":
		cmdWaitLayout(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "clipboard":
		cmdWaitClipboard(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "hook":
		cmdWaitHook(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "content":
		cmdWaitFor(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "ready":
		cmdWaitReady(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "vt-idle":
		cmdWaitVTIdle(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "idle":
		cmdWaitIdle(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "busy":
		cmdWaitBusy(waitSubcommandContext(ctx, ctx.Args[1:]))
	case "ui":
		cmdWaitUI(waitSubcommandContext(ctx, ctx.Args[1:]))
	default:
		ctx.replyErr(fmt.Sprintf("unknown wait kind: %s", ctx.Args[0]))
	}
}

func cmdGeneration(ctx *CommandContext) {
	gen := ctx.Sess.generation.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdLayoutJSON(ctx *CommandContext) {
	snap, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*proto.LayoutSnapshot, error) {
		return sess.snapshotLayout(nil), nil
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(string(data) + "\n")
}

func cmdWaitLayout(ctx *CommandContext) {
	afterGen, afterSet, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	var (
		gen uint64
		ok  bool
	)
	if afterSet {
		gen, ok = ctx.Sess.waitGeneration(afterGen, timeout)
	} else {
		gen, ok = ctx.Sess.waitGenerationAfterCurrent(timeout)
	}
	if !ok {
		ctx.replyErr(fmt.Sprintf("timeout waiting for generation > %d (current: %d)", afterGen, gen))
		return
	}
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdClipboardGen(ctx *CommandContext) {
	gen := ctx.Sess.clipboardGen.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdWaitClipboard(ctx *CommandContext) {
	afterGen, afterSet, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	var (
		data string
		ok   bool
	)
	if afterSet {
		data, ok = ctx.Sess.waitClipboard(afterGen, timeout)
	} else {
		data, ok = ctx.Sess.waitClipboardAfterCurrent(timeout)
	}
	if !ok {
		ctx.replyErr("timeout waiting for clipboard event")
		return
	}
	ctx.reply(data + "\n")
}

func cmdHookGen(ctx *CommandContext) {
	gen := ctx.Sess.hookGen.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdUIGen(ctx *CommandContext) {
	requestedClientID, err := parseUIGenArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	client, err := ctx.Sess.queryUIClient(requestedClientID, "")
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("%d\n", client.currentGen))
}

func resolveWaitHookPane(ctx *CommandContext, ref string) (resolvedPaneRef, error) {
	if ref == "" {
		return resolvedPaneRef{}, nil
	}
	return ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, ref)
}

func cmdWaitHook(ctx *CommandContext) {
	eventName, paneName, afterGen, timeout, err := parseWaitHookArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	pane, err := resolveWaitHookPane(ctx, paneName)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneName = pane.paneName
	var (
		record hookResultRecord
		ok     bool
	)
	if hasAfterFlag(ctx.Args) {
		record, ok = ctx.Sess.waitHookForPane(afterGen, eventName, pane.paneID, paneName, timeout)
	} else {
		record, ok = ctx.Sess.waitHookForPaneAfterCurrent(eventName, pane.paneID, paneName, timeout)
	}
	if !ok {
		target := eventName
		if paneName != "" {
			target += " on " + paneName
		}
		ctx.replyErr(fmt.Sprintf("timeout waiting for hook %s", target))
		return
	}
	status := "success"
	if !record.Success {
		status = "failure"
	}
	ctx.reply(fmt.Sprintf("%d %s %s %s\n", record.Generation, record.Event, record.PaneName, status))
}

func cmdWaitFor(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: wait content <pane> <substring> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	substr := ctx.Args[1]
	timeout, err := parseTimeout(ctx.Args, 2, 10*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	start, err := ctx.Sess.beginPaneOutputWait(paneID, substr)
	if err != nil {
		ctx.replyErr("session shutting down")
		return
	}
	if !start.exists {
		ctx.replyErr(fmt.Sprintf("pane %q disappeared while waiting for %q", paneRef, substr))
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, start.ch)
	if start.matched {
		ctx.reply("matched\n")
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-start.ch:
			if ctx.Sess.paneScreenContains(paneID, substr) {
				ctx.reply("matched\n")
				return
			}
		case <-timer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %q in %s", substr, paneRef))
			return
		}
	}
}

func cmdWaitIdle(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: wait idle <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	checkIdle := func() (bool, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return false, err
		}
		if pane == nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
		}
		if !pane.AgentStatus().Idle {
			return false, nil
		}
		return true, nil
	}

	res := ctx.Sess.enqueueEventSubscribe(eventFilter{Types: []string{EventIdle}, PaneID: paneID}, true)
	if res.sub == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	if len(res.initialState) > 0 {
		idle, err := checkIdle()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		if idle {
			ctx.reply("idle\n")
			return
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-res.sub.ch:
			idle, err := checkIdle()
			if err != nil {
				ctx.replyErr(err.Error())
				return
			}
			if idle {
				ctx.reply("idle\n")
				return
			}
		case <-timer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become idle", paneRef))
			return
		}
	}
}

func cmdWaitBusy(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: wait busy <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	checkBusy := func() (bool, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return false, err
		}
		if pane == nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return !pane.AgentStatus().Idle, nil
	}
	checkBusyStatus := func() (mux.AgentStatus, error) {
		pane, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return mux.AgentStatus{}, err
		}
		if pane == nil {
			return mux.AgentStatus{}, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return pane.AgentStatus(), nil
	}

	outputCh := ctx.Sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	busy, err := checkBusy()
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if busy {
		st, err := checkBusyStatus()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		candidatePID := waitBusyForegroundPID(st)
		if candidatePID != 0 {
			time.Sleep(50 * time.Millisecond)
			st2, err := checkBusyStatus()
			if err != nil {
				ctx.replyErr(err.Error())
				return
			}
			if _, ready := waitBusyReady(candidatePID, st2); ready {
				ctx.reply("busy\n")
				return
			}
		}
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	candidatePID := 0

	for {
		select {
		case <-outputCh:
		case <-ticker.C:
		case <-timeoutTimer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become busy", paneRef))
			return
		}

		st, err := checkBusyStatus()
		if err != nil {
			ctx.replyErr(err.Error())
			return
		}
		nextPID, ready := waitBusyReady(candidatePID, st)
		if ready {
			ctx.reply("busy\n")
			return
		}
		candidatePID = nextPID
	}
}

func waitForUIEvent(sess *Session, requestedClientID, eventName string, afterGen uint64, afterSet bool, timeout time.Duration) (string, error) {
	if !proto.IsKnownUIEvent(eventName) {
		return "", errUnknownUIEvent(eventName)
	}

	subscription, err := sess.enqueueUIWaitSubscribe(requestedClientID, eventName)
	if err != nil {
		return "", err
	}
	defer sess.enqueueEventUnsubscribe(subscription.sub)

	if subscription.currentMatch && (!afterSet || subscription.currentGen > afterGen) {
		return subscription.clientID, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-subscription.sub.ch:
		return subscription.clientID, nil
	case <-timer.C:
		return "", fmt.Errorf("timeout waiting for %s on %s", eventName, subscription.clientID)
	}
}

func waitForNextUIEvent(sess *Session, client uiClientSnapshot, eventName string, timeout time.Duration) error {
	_, err := waitForUIEvent(sess, client.clientID, eventName, client.currentGen, true, timeout)
	return err
}

func cmdWaitUI(ctx *CommandContext) {
	eventName, requestedClientID, afterGen, afterSet, timeout, err := parseWaitUIArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if _, err := waitForUIEvent(ctx.Sess, requestedClientID, eventName, afterGen, afterSet, timeout); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(eventName + "\n")
}

func hasAfterFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--after" {
			return true
		}
	}
	return false
}
