package server

import (
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
)

const waitReadyUsage = "usage: wait ready <pane> [--timeout <duration>]"

type waitReadyOptions struct {
	timeout time.Duration
}

type sendKeysWaitTarget int

const (
	sendKeysNoWait sendKeysWaitTarget = iota
	sendKeysWaitReady
	sendKeysWaitInputIdle
)

type sendKeysOptions struct {
	waitTarget  sendKeysWaitTarget
	waitTimeout time.Duration
	delayFinal  time.Duration
	hexMode     bool
	keys        []string
}

type paneReadyState struct {
	pane   *mux.Pane
	vtIdle vtIdleWaitState
}

func cmdWaitReady(ctx *CommandContext) {
	paneRef, opts, err := parseWaitReadyArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	if err := waitForPaneReady(ctx.Sess, paneRef, pane, opts); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply("ready\n")
}

func parseWaitReadyArgs(args []string) (string, waitReadyOptions, error) {
	if len(args) < 1 {
		return "", waitReadyOptions{}, fmt.Errorf(waitReadyUsage)
	}

	opts := waitReadyOptions{timeout: 10 * time.Second}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--continue-known-dialogs":
			return "", waitReadyOptions{}, fmt.Errorf("wait ready: --continue-known-dialogs was removed; ready now waits for vt-idle + idle")
		case "--timeout":
			if i+1 >= len(args) {
				return "", waitReadyOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return "", waitReadyOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.timeout = timeout
		default:
			return "", waitReadyOptions{}, fmt.Errorf("unknown flag: %s", args[i])
		}
	}

	return args[0], opts, nil
}

func parseSendKeysArgs(args []string) (sendKeysOptions, error) {
	opts := sendKeysOptions{waitTimeout: 10 * time.Second}
	timeoutSet := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--wait":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --wait")
			}
			i++
			switch args[i] {
			case "ready":
				opts.waitTarget = sendKeysWaitReady
			case "ui=input-idle":
				opts.waitTarget = sendKeysWaitInputIdle
			default:
				return sendKeysOptions{}, fmt.Errorf("send-keys: unsupported --wait target %q (want ready or ui=input-idle)", args[i])
			}
			i++
		case "--wait-ready":
			return sendKeysOptions{}, fmt.Errorf("send-keys: --wait-ready was removed; use --wait ready")
		case "--continue-known-dialogs":
			return sendKeysOptions{}, fmt.Errorf("send-keys: --continue-known-dialogs was removed; ready now waits for vt-idle + idle")
		case "--timeout":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --timeout")
			}
			i++
			timeout, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid timeout: %s", args[i])
			}
			opts.waitTimeout = timeout
			timeoutSet = true
			i++
		case "--delay-final":
			if i+1 >= len(args) {
				return sendKeysOptions{}, fmt.Errorf("missing value for --delay-final")
			}
			i++
			delay, err := time.ParseDuration(args[i])
			if err != nil {
				return sendKeysOptions{}, fmt.Errorf("invalid delay-final: %s", args[i])
			}
			opts.delayFinal = delay
			i++
		case "--hex":
			opts.hexMode = true
			i++
		default:
			opts.keys = append(opts.keys, args[i:]...)
			i = len(args)
		}
	}

	if timeoutSet && opts.waitTarget == sendKeysNoWait {
		return sendKeysOptions{}, fmt.Errorf("send-keys: --timeout requires --wait ready or --wait ui=input-idle")
	}
	return opts, nil
}

func waitForPaneReady(sess *Session, paneRef string, paneRefData resolvedPaneRef, opts waitReadyOptions) error {
	outputCh := sess.enqueuePaneOutputSubscribe(paneRefData.paneID)
	if outputCh == nil {
		return fmt.Errorf("session shutting down")
	}
	defer sess.enqueuePaneOutputUnsubscribe(paneRefData.paneID, outputCh)

	clk := sess.clock()
	timeoutTimer := clk.NewTimer(opts.timeout)
	defer stopTimer(timeoutTimer)

	settleTimer := clk.NewTimer(opts.timeout)
	stopTimer(settleTimer)
	defer stopTimer(settleTimer)
	settleActive := false

	syncReady := func() (bool, error) {
		state, err := queryPaneReadyState(sess, paneRefData.paneID)
		if err != nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become ready", paneRef)
		}

		remaining := state.vtIdle.remaining(sess.vtIdleSettle(), clk.Now())
		if remaining > 0 {
			if settleActive {
				resetTimer(settleTimer, remaining)
			} else {
				settleTimer.Reset(remaining)
				settleActive = true
			}
			return false, nil
		}

		if settleActive {
			stopTimer(settleTimer)
			settleActive = false
		}

		return state.pane.AgentStatus().Idle, nil
	}

	ready, err := syncReady()
	if err != nil {
		return err
	}
	if ready {
		return nil
	}

	for {
		var settleCh <-chan time.Time
		if settleActive {
			settleCh = settleTimer.C()
		}

		select {
		case <-outputCh:
		case <-settleCh:
			settleActive = false
		case <-timeoutTimer.C():
			return fmt.Errorf("timeout waiting for %s to become ready", paneRef)
		}

		ready, err := syncReady()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
	}
}

func queryPaneReadyState(sess *Session, paneID uint32) (paneReadyState, error) {
	state, err := enqueueSessionQuery(sess, func(sess *Session) (paneReadyState, error) {
		pane := sess.findPaneByID(paneID)
		if pane == nil {
			return paneReadyState{}, nil
		}

		var lastOutput time.Time
		hasLastOutput := false
		if sess.vtIdle != nil {
			lastOutput, hasLastOutput = sess.vtIdle.LastOutput(paneID)
		}

		return paneReadyState{
			pane: pane,
			vtIdle: vtIdleWaitState{
				createdAt:     pane.CreatedAt(),
				lastOutput:    lastOutput,
				hasLastOutput: hasLastOutput,
				exists:        true,
			},
		}, nil
	})
	if err != nil {
		return paneReadyState{}, err
	}
	if state.pane == nil {
		return paneReadyState{}, fmt.Errorf("pane missing")
	}
	return state, nil
}
