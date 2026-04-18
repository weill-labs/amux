package server

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	waitcmd "github.com/weill-labs/amux/internal/server/commands/wait"
)

const (
	waitCommandUsage   = "usage: wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui> ..."
	cursorCommandUsage = "usage: cursor <layout|clipboard|ui> [--client <id>]"
)

type waitCommandContext struct {
	*CommandContext
}

func waitRemoteForward(ctx *CommandContext, args []string) (commandpkg.Result, bool) {
	if len(args) == 0 {
		return commandpkg.Result{}, false
	}

	var paneArgIndex int
	switch args[0] {
	case "content":
		if len(args) < 2 {
			return commandpkg.Result{}, false
		}
		paneArgIndex = 1
	case "ready", "idle", "exited", "busy":
		if len(args) < 2 {
			return commandpkg.Result{}, false
		}
		paneArgIndex = 1
	default:
		return commandpkg.Result{}, false
	}

	ref, err := ctx.Sess.queryPaneRef(args[paneArgIndex])
	if err != nil {
		return commandpkg.Result{Err: err}, true
	}
	if ref.Host == "" {
		return commandpkg.Result{}, false
	}
	return remoteCommandResult(ctx.Sess, ref.Host, "wait", rewritePaneRefArg(args, paneArgIndex, ref.Pane)), true
}

func (ctx waitCommandContext) Generation() uint64 {
	return ctx.Sess.generation.Load()
}

func (ctx waitCommandContext) LayoutJSON() (string, error) {
	snap, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (*proto.LayoutSnapshot, error) {
		return sess.snapshotLayout(nil), nil
	})
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func (ctx waitCommandContext) WaitLayout(afterGen uint64, afterSet bool, timeout time.Duration) (uint64, bool) {
	if afterSet {
		return ctx.Sess.waitGeneration(afterGen, timeout)
	}
	return ctx.Sess.waitGenerationAfterCurrent(timeout)
}

func (ctx waitCommandContext) ClipboardGeneration() uint64 {
	return ctx.Sess.clipboardGeneration()
}

func (ctx waitCommandContext) WaitClipboard(afterGen uint64, afterSet bool, timeout time.Duration) (string, bool) {
	if afterSet {
		return ctx.Sess.waitClipboard(afterGen, timeout)
	}
	return ctx.Sess.waitClipboardAfterCurrent(timeout)
}

func (ctx waitCommandContext) WaitCheckpoint(afterGen uint64, afterSet bool, timeout time.Duration) (waitcmd.CheckpointRecord, bool) {
	var (
		record crashCheckpointRecord
		ok     bool
	)
	if afterSet {
		record, ok = ctx.Sess.waitCrashCheckpoint(afterGen, timeout)
	} else {
		record, ok = ctx.Sess.waitCrashCheckpointAfterCurrent(timeout)
	}
	return waitcmd.CheckpointRecord{
		Generation: record.generation,
		Path:       record.path,
	}, ok
}

func (ctx waitCommandContext) UIGeneration(requestedClientID string) (uint64, error) {
	client, err := ctx.Sess.queryUIClient(requestedClientID, "")
	if err != nil {
		return 0, err
	}
	return client.currentGen, nil
}

func (ctx waitCommandContext) WaitContent(actorPaneID uint32, paneRef, substr string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, paneRef)
	if err != nil {
		return err
	}
	paneID := pane.paneID

	start, err := ctx.Sess.beginPaneOutputWait(paneID, substr)
	if err != nil {
		return fmt.Errorf("session shutting down")
	}
	if !start.exists {
		return fmt.Errorf("pane %q disappeared while waiting for %q", paneRef, substr)
	}
	defer ctx.Sess.enqueuePaneOutputUnsubscribe(paneID, start.ch)
	if start.matched {
		return nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-start.ch:
			if ctx.Sess.paneScreenContains(paneID, substr) {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for %q in %s", substr, paneRef)
		}
	}
}

func (ctx waitCommandContext) WaitExited(actorPaneID uint32, paneRef string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, paneRef)
	if err != nil {
		return err
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
			return false, fmt.Errorf("pane %q disappeared while waiting to become exited", paneRef)
		}
		if !pane.ForegroundJobState().Idle {
			return false, nil
		}
		return true, nil
	}

	res := ctx.Sess.enqueueEventSubscribe(eventFilter{Types: []string{EventExited}, PaneID: paneID}, true)
	if res.sub == nil {
		return fmt.Errorf("session shutting down")
	}
	defer ctx.Sess.enqueueEventUnsubscribe(res.sub)

	if len(res.initialState) > 0 {
		idle, err := checkIdle()
		if err != nil {
			return err
		}
		if idle {
			return nil
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-res.sub.Ch:
			idle, err := checkIdle()
			if err != nil {
				return err
			}
			if idle {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timeout waiting for %s to become exited", paneRef)
		}
	}
}

func (ctx waitCommandContext) WaitBusy(actorPaneID uint32, paneRef string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneBusy(ctx.Sess, pane.paneID, paneRef, timeout)
}

func (ctx waitCommandContext) WaitUI(eventName, requestedClientID string, afterGen uint64, afterSet bool, timeout time.Duration) error {
	_, err := waitForUIEvent(ctx.Sess, requestedClientID, eventName, afterGen, afterSet, timeout)
	return err
}

func (ctx waitCommandContext) WaitReady(actorPaneID uint32, args []string) error {
	paneRef, opts, err := parseWaitReadyArgs(args)
	if err != nil {
		return err
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneReady(ctx.Sess, paneRef, pane, opts)
}

func (ctx waitCommandContext) WaitIdle(actorPaneID uint32, args []string) error {
	idle := ctx.Sess.ensureIdleTracker()
	paneRef, opts, err := parseWaitIdleArgsWithDefaults(args, idle.Settle(), idle.Timeout())
	if err != nil {
		return err
	}

	pane, err := ctx.Sess.queryResolvedPaneForActor(actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneIdle(ctx.Sess, paneRef, pane.paneID, opts)
}

func parseWaitArgs(args []string) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return waitcmd.ParseWaitArgs(args)
}

func parseWaitArgsWithDefault(args []string, defaultTimeout time.Duration) (afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return waitcmd.ParseWaitArgsWithDefault(args, defaultTimeout)
}

func parseTimeout(args []string, startIdx int, defaultTimeout time.Duration) (time.Duration, error) {
	return waitcmd.ParseTimeout(args, startIdx, defaultTimeout)
}

func parseUIGenArgs(args []string) (clientID string, err error) {
	return waitcmd.ParseUIGenArgs(args)
}

func parseWaitUIArgs(args []string) (eventName, clientID string, afterGen uint64, afterSet bool, timeout time.Duration, err error) {
	return waitcmd.ParseWaitUIArgs(args)
}

func waitBusyForegroundProcessGroup(status mux.ForegroundJobState) int {
	return waitcmd.WaitBusyForegroundProcessGroup(status)
}

func waitBusyReady(candidateProcessGroup int, status mux.ForegroundJobState) (nextProcessGroup int, ready bool) {
	return waitcmd.WaitBusyReady(candidateProcessGroup, status)
}

func cmdCursor(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.Cursor(waitCommandContext{ctx}, ctx.Args))
}

func cmdWait(ctx *CommandContext) {
	if res, ok := waitRemoteForward(ctx, ctx.Args); ok {
		ctx.applyCommandResult(res)
		return
	}
	ctx.applyCommandResult(waitcmd.Wait(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func cmdLayoutJSON(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.LayoutJSON(waitCommandContext{ctx}, ctx.Args))
}

func cmdWaitFor(ctx *CommandContext) {
	if forwardedArgs := append([]string{"content"}, ctx.Args...); len(ctx.Args) > 0 {
		if res, ok := waitRemoteForward(ctx, forwardedArgs); ok {
			ctx.applyCommandResult(res)
			return
		}
	}
	ctx.applyCommandResult(waitcmd.WaitFor(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func cmdWaitBusy(ctx *CommandContext) {
	if forwardedArgs := append([]string{"busy"}, ctx.Args...); len(ctx.Args) > 0 {
		if res, ok := waitRemoteForward(ctx, forwardedArgs); ok {
			ctx.applyCommandResult(res)
			return
		}
	}
	ctx.applyCommandResult(waitcmd.WaitBusy(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func waitForPaneBusy(sess *Session, paneID uint32, paneRef string, timeout time.Duration) error {
	checkBusy := func() (bool, error) {
		pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return false, err
		}
		if pane == nil {
			return false, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return !pane.ForegroundJobState().Idle, nil
	}
	checkBusyStatus := func() (mux.ForegroundJobState, error) {
		pane, err := enqueueSessionQuery(sess, func(sess *Session) (*mux.Pane, error) {
			return sess.findPaneByID(paneID), nil
		})
		if err != nil {
			return mux.ForegroundJobState{}, err
		}
		if pane == nil {
			return mux.ForegroundJobState{}, fmt.Errorf("pane %q disappeared while waiting to become busy", paneRef)
		}
		return pane.ForegroundJobState(), nil
	}

	outputCh := sess.enqueuePaneOutputSubscribe(paneID)
	if outputCh == nil {
		return fmt.Errorf("session shutting down")
	}
	defer sess.enqueuePaneOutputUnsubscribe(paneID, outputCh)

	busy, err := checkBusy()
	if err != nil {
		return err
	}
	if busy {
		st, err := checkBusyStatus()
		if err != nil {
			return err
		}
		candidateProcessGroup := waitBusyForegroundProcessGroup(st)
		if candidateProcessGroup != 0 {
			time.Sleep(50 * time.Millisecond)
			st2, err := checkBusyStatus()
			if err != nil {
				return err
			}
			if _, ready := waitBusyReady(candidateProcessGroup, st2); ready {
				return nil
			}
		}
	}

	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	candidateProcessGroup := 0

	for {
		select {
		case <-outputCh:
		case <-ticker.C:
		case <-timeoutTimer.C:
			return fmt.Errorf("timeout waiting for %s to become busy", paneRef)
		}

		st, err := checkBusyStatus()
		if err != nil {
			return err
		}
		nextProcessGroup, ready := waitBusyReady(candidateProcessGroup, st)
		if ready {
			return nil
		}
		candidateProcessGroup = nextProcessGroup
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
	case <-subscription.sub.Ch:
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
	ctx.applyCommandResult(waitcmd.WaitUI(waitCommandContext{ctx}, ctx.Args))
}

func hasAfterFlag(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--after" {
			return true
		}
	}
	return false
}
