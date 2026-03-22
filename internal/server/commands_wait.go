package server

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	waitcmd "github.com/weill-labs/amux/internal/server/commands/wait"
)

func parseWaitArgs(args []string) (afterGen uint64, timeout time.Duration, err error) {
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

func cmdGeneration(ctx *CommandContext) {
	gen := ctx.Sess.generation.Load()
	ctx.reply(fmt.Sprintf("%d\n", gen))
}

func cmdWaitLayout(ctx *CommandContext) {
	afterGen, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	gen, ok := ctx.Sess.waitGeneration(afterGen, timeout)
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
	afterGen, timeout, err := parseWaitArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	data, ok := ctx.Sess.waitClipboard(afterGen, timeout)
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

func resolveWaitHookPaneName(ctx *CommandContext, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	pane, err := ctx.Sess.queryResolvedPane(ref)
	if err != nil {
		return "", err
	}
	return pane.paneName, nil
}

func cmdWaitHook(ctx *CommandContext) {
	eventName, paneName, afterGen, timeout, err := parseWaitHookArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneName, err = resolveWaitHookPaneName(ctx, paneName)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	record, ok := ctx.Sess.waitHook(afterGen, eventName, paneName, timeout)
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
		ctx.replyErr("usage: wait-for <pane> <substring> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	substr := ctx.Args[1]
	timeout, err := parseTimeout(ctx.Args, 2, 10*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID

	if ctx.Sess.paneScreenContains(paneID, substr) {
		ctx.reply("matched\n")
		return
	}

	ch := ctx.Sess.enqueuePaneOutputSubscribe(paneID)
	if ch == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ch:
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
		ctx.replyErr("usage: wait-idle <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	paneID := pane.paneID
	paneName := pane.paneName

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

	res := ctx.Sess.enqueueEventSubscribe(eventFilter{Types: []string{EventIdle}, PaneName: paneName}, false)
	if res.sub == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	if ctx.Sess.idle.IsIdle(paneID) {
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
		ctx.replyErr("usage: wait-busy <pane> [--timeout <duration>]")
		return
	}
	paneRef := ctx.Args[0]
	timeout, err := parseTimeout(ctx.Args, 1, 5*time.Second)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPane(paneRef)
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

func cmdWaitUI(ctx *CommandContext) {
	eventName, requestedClientID, afterGen, afterSet, timeout, err := parseWaitUIArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if !proto.IsKnownUIEvent(eventName) {
		ctx.replyErr(errUnknownUIEvent(eventName).Error())
		return
	}

	subscription, err := ctx.Sess.enqueueUIWaitSubscribe(requestedClientID, eventName)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	defer ctx.Sess.enqueueEventUnsubscribe(subscription.sub)

	if subscription.currentMatch && (!afterSet || subscription.currentGen > afterGen) {
		ctx.reply(eventName + "\n")
		return
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-subscription.sub.ch:
		ctx.reply(eventName + "\n")
	case <-timer.C:
		ctx.replyErr(fmt.Sprintf("timeout waiting for %s on %s", eventName, subscription.clientID))
	}
}
