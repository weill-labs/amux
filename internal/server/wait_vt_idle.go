package server

import (
	"fmt"
	"time"
)

const waitVTIdleUsage = "usage: wait-vt-idle <pane> [--settle <duration>] [--timeout <duration>]"

type waitVTIdleOptions struct {
	settle  time.Duration
	timeout time.Duration
}

func parseWaitVTIdleArgs(args []string) (string, waitVTIdleOptions, error) {
	if len(args) < 1 {
		return "", waitVTIdleOptions{}, fmt.Errorf(waitVTIdleUsage)
	}

	opts := waitVTIdleOptions{
		settle:  DefaultVTIdleSettle,
		timeout: DefaultVTIdleTimeout,
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--settle":
			if i+1 >= len(args) {
				return "", waitVTIdleOptions{}, fmt.Errorf("missing value for --settle")
			}
			i++
			settle, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitVTIdleOptions{}, fmt.Errorf("invalid settle: %s", args[i])
			}
			opts.settle = settle
		case "--timeout":
			if i+1 >= len(args) {
				return "", waitVTIdleOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitVTIdleOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.timeout = timeout
		default:
			return "", waitVTIdleOptions{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	return args[0], opts, nil
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

func cmdWaitVTIdle(ctx *CommandContext) {
	paneRef, opts, err := parseWaitVTIdleArgs(ctx.Args)
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

	outputCh := ctx.Sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		ctx.replyErr("session shutting down")
		return
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	state, err := ctx.Sess.queryVTIdleWaitState(paneID)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if !state.exists {
		ctx.replyErr(fmt.Sprintf("pane %q disappeared while waiting to become vt-idle", paneRef))
		return
	}

	settleTimer := time.NewTimer(state.remaining(opts.settle, time.Now()))
	defer settleTimer.Stop()

	timeoutTimer := time.NewTimer(opts.timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case <-outputCh:
			resetTimer(settleTimer, opts.settle)
		case <-settleTimer.C:
			state, err := ctx.Sess.queryVTIdleWaitState(paneID)
			if err != nil {
				ctx.replyErr(err.Error())
				return
			}
			if !state.exists {
				ctx.replyErr(fmt.Sprintf("pane %q disappeared while waiting to become vt-idle", paneRef))
				return
			}

			remaining := state.remaining(opts.settle, time.Now())
			if remaining == 0 {
				ctx.reply("vt-idle\n")
				return
			}
			settleTimer.Reset(remaining)
		case <-timeoutTimer.C:
			ctx.replyErr(fmt.Sprintf("timeout waiting for %s to become vt-idle", paneRef))
			return
		}
	}
}
