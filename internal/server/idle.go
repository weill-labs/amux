package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/config"
	cmdflags "github.com/weill-labs/amux/internal/server/commands/flags"
	waitcmd "github.com/weill-labs/amux/internal/server/commands/wait"
)

type paneIdleStatus struct {
	idle          bool
	idleSince     time.Time
	lastOutput    time.Time
	hasLastOutput bool
}

func (s *Session) paneIdleStatus(paneID uint32, createdAt, now time.Time) paneIdleStatus {
	return s.ensureIdleTracker().PaneStatus(paneID, createdAt, now)
}

func (s paneIdleStatus) listDisplay(now, createdAt time.Time) string {
	if !s.idle {
		return "--"
	}
	base := createdAt
	if s.hasLastOutput {
		base = s.lastOutput
	}
	if base.After(now) {
		return "0s ago"
	}
	return fmt.Sprintf("%ds ago", int(now.Sub(base)/time.Second))
}

type idleWaitState struct {
	createdAt     time.Time
	lastOutput    time.Time
	hasLastOutput bool
	exists        bool
}

func (s *Session) queryIdleWaitState(paneID uint32) (idleWaitState, error) {
	return enqueueSessionQuery(s, func(sess *Session) (idleWaitState, error) {
		pane := sess.findPaneByID(paneID)
		if pane == nil {
			return idleWaitState{}, nil
		}
		return sess.ensureIdleTracker().WaitState(paneID, pane.CreatedAt()), nil
	})
}

func (s idleWaitState) remaining(settle time.Duration, now time.Time) time.Duration {
	base := s.createdAt
	if s.hasLastOutput {
		base = s.lastOutput
	}
	deadline := base.Add(settle)
	if deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}

const waitIdleUsage = "usage: wait idle <pane> [--settle <duration>] [--timeout <duration>]"

type waitIdleOptions struct {
	settle  time.Duration
	timeout time.Duration
}

func parseWaitIdleArgs(args []string) (string, waitIdleOptions, error) {
	return parseWaitIdleArgsWithDefaults(args, config.VTIdleSettle, config.VTIdleTimeout)
}

func parseWaitIdleArgsWithDefaults(args []string, settleDefault, timeoutDefault time.Duration) (string, waitIdleOptions, error) {
	if len(args) < 1 {
		return "", waitIdleOptions{}, errors.New(waitIdleUsage)
	}

	flags, err := cmdflags.ParseCommandFlags(args[1:], []cmdflags.FlagSpec{
		{Name: "--settle", Type: cmdflags.FlagTypeDuration, Default: settleDefault},
		{Name: "--timeout", Type: cmdflags.FlagTypeDuration, Default: timeoutDefault},
	})
	if err != nil {
		return "", waitIdleOptions{}, err
	}
	positionals := flags.Positionals()
	if len(positionals) > 0 {
		return "", waitIdleOptions{}, fmt.Errorf("unknown flag: %s", positionals[0])
	}

	return args[0], waitIdleOptions{
		settle:  flags.Duration("--settle"),
		timeout: flags.Duration("--timeout"),
	}, nil
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

	state, err := sess.queryIdleWaitState(paneID)
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
			state, err := sess.queryIdleWaitState(paneID)
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
			// This receive drained settleTimer.C, so Reset is safe here.
			settleTimer.Reset(remaining)
		case <-timeoutTimer.C():
			return fmt.Errorf("timeout waiting for %s to become idle", paneRef)
		}
	}
}

func cmdWaitIdle(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.WaitIdle(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}
