package server

import (
	"fmt"
	"time"
)

const waitIdleUsage = "usage: wait idle <pane> [--settle <duration>] [--timeout <duration>]"

type waitIdleOptions struct {
	settle  time.Duration
	timeout time.Duration
}

type waitVTIdleOptions = waitIdleOptions

const waitVTIdleUsage = waitIdleUsage

func parseWaitIdleArgs(args []string) (string, waitIdleOptions, error) {
	if len(args) < 1 {
		return "", waitIdleOptions{}, fmt.Errorf(waitIdleUsage)
	}

	opts := waitIdleOptions{
		settle:  DefaultVTIdleSettle,
		timeout: DefaultVTIdleTimeout,
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--settle":
			if i+1 >= len(args) {
				return "", waitIdleOptions{}, fmt.Errorf("missing value for --settle")
			}
			i++
			settle, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitIdleOptions{}, fmt.Errorf("invalid settle: %s", args[i])
			}
			opts.settle = settle
		case "--timeout":
			if i+1 >= len(args) {
				return "", waitIdleOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitIdleOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.timeout = timeout
		default:
			return "", waitIdleOptions{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	return args[0], opts, nil
}

func parseWaitVTIdleArgs(args []string) (string, waitVTIdleOptions, error) {
	return parseWaitIdleArgs(args)
}

func resetTimer(timer Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}
	timer.Reset(d)
}

func stopTimer(timer Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}
}

func waitForPaneIdle(sess *Session, paneRef string, paneID uint32, opts waitIdleOptions) error {
	outputCh := sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		return fmt.Errorf("session shutting down")
	}
	defer sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	state, err := sess.queryVTIdleWaitState(paneID)
	if err != nil {
		return err
	}
	if !state.exists {
		return fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
	}

	clk := sess.clock()
	settleTimer := clk.NewTimer(state.remaining(opts.settle, clk.Now()))
	defer settleTimer.Stop()

	timeoutTimer := clk.NewTimer(opts.timeout)
	defer timeoutTimer.Stop()

	for {
		select {
		case <-outputCh:
			resetTimer(settleTimer, opts.settle)
		case <-settleTimer.C():
			state, err := sess.queryVTIdleWaitState(paneID)
			if err != nil {
				return err
			}
			if !state.exists {
				return fmt.Errorf("pane %q disappeared while waiting to become idle", paneRef)
			}

			remaining := state.remaining(opts.settle, clk.Now())
			if remaining == 0 {
				return nil
			}
			settleTimer.Reset(remaining)
		case <-timeoutTimer.C():
			return fmt.Errorf("timeout waiting for %s to become idle", paneRef)
		}
	}
}

func cmdWaitIdle(ctx *CommandContext) {
	paneRef, opts, err := parseWaitIdleArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := waitForPaneIdle(ctx.Sess, paneRef, pane.paneID, opts); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply("idle\n")
}

func cmdWaitVTIdle(ctx *CommandContext) {
	cmdWaitIdle(ctx)
}
