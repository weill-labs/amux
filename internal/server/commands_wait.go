package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/proto"
	waitcmd "github.com/weill-labs/amux/internal/server/commands/wait"
)

const (
	waitCommandUsage   = "usage: wait <idle|busy|exited|ready|content|layout|clipboard|checkpoint|ui|msg> ..."
	cursorCommandUsage = "usage: cursor <layout|clipboard|ui> [--client <id>]"
)

type waitCommandContext struct {
	*CommandContext
}

func (ctx waitCommandContext) Generation() uint64 {
	return ctx.Sess.generation.Load()
}

func (ctx waitCommandContext) LayoutJSON() (string, error) {
	snap, err := enqueueSessionQueryOnState(ctx.context(), ctx.Sess, func(sess *Session) (*proto.LayoutSnapshot, error) {
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
	client, err := ctx.Sess.queryUIClientContext(ctx.context(), requestedClientID, "")
	if err != nil {
		return 0, err
	}
	return client.currentGen, nil
}

func (ctx waitCommandContext) WaitContent(actorPaneID uint32, paneRef, substr string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, paneRef)
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
		case <-ctx.context().Done():
			return ctx.context().Err()
		}
	}
}

func (ctx waitCommandContext) WaitExited(actorPaneID uint32, paneRef string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, paneRef)
	if err != nil {
		return err
	}
	paneID := pane.paneID

	checkIdle := func() (bool, error) {
		pane, err := enqueueSessionQueryOnState(ctx.context(), ctx.Sess, func(sess *Session) (*mux.Pane, error) {
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

	res := ctx.Sess.enqueueEventSubscribe(ctx.context(), eventFilter{Types: []string{EventExited}, PaneID: paneID}, true)
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
		case <-ctx.context().Done():
			return ctx.context().Err()
		}
	}
}

func (ctx waitCommandContext) WaitBusy(actorPaneID uint32, paneRef string, timeout time.Duration) error {
	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneBusy(ctx.context(), ctx.Sess, pane.paneID, paneRef, timeout)
}

func (ctx waitCommandContext) WaitUI(eventName, requestedClientID string, afterGen uint64, afterSet bool, timeout time.Duration) error {
	_, err := waitForUIEvent(ctx.context(), ctx.Sess, requestedClientID, eventName, afterGen, afterSet, timeout)
	return err
}

func (ctx waitCommandContext) WaitReady(actorPaneID uint32, args []string) error {
	paneRef, opts, err := parseWaitReadyArgs(args)
	if err != nil {
		return err
	}

	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneReady(ctx.context(), ctx.Sess, paneRef, pane, opts)
}

func (ctx waitCommandContext) WaitIdle(actorPaneID uint32, args []string) error {
	idle := ctx.Sess.ensureIdleTracker()
	paneRef, opts, err := parseWaitIdleArgsWithDefaults(args, idle.Settle(), idle.Timeout())
	if err != nil {
		return err
	}

	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, paneRef)
	if err != nil {
		return err
	}
	return waitForPaneIdle(ctx.context(), ctx.Sess, paneRef, pane.paneID, opts)
}

func (ctx waitCommandContext) WaitMessage(actorPaneID uint32, opts waitcmd.MessageWaitOptions) (proto.MailboxMessageSummary, error) {
	pane, err := ctx.Sess.queryResolvedPaneForActorContext(ctx.context(), actorPaneID, opts.PaneRef)
	if err != nil {
		return proto.MailboxMessageSummary{}, err
	}

	sub := ctx.Sess.enqueueMailboxWaitSubscribe(ctx.context(), pane.paneID)
	if sub.sub == nil {
		return proto.MailboxMessageSummary{}, fmt.Errorf("session shutting down")
	}
	defer ctx.Sess.enqueueEventUnsubscribe(sub.sub)

	matchOpts := mailboxWaitOptions{
		topic:          opts.Topic,
		afterMessageID: opts.AfterMessageID,
		afterEventSeq:  opts.AfterEventSeq,
	}
	for _, data := range sub.initialState {
		ev, ok := mailboxEventSummaryFromJSON(data)
		if ok && mailboxEventMatchesWait(ev, matchOpts) {
			return *ev.Message, nil
		}
	}
	if !sub.targetExists {
		return proto.MailboxMessageSummary{}, fmt.Errorf("pane %q disappeared while waiting for message", opts.PaneRef)
	}

	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	for {
		select {
		case data := <-sub.sub.Ch:
			ev, ok := mailboxEventSummaryFromJSON(data)
			if !ok {
				continue
			}
			if ev.Type == EventPaneExit {
				return proto.MailboxMessageSummary{}, fmt.Errorf("pane %q disappeared while waiting for message", opts.PaneRef)
			}
			if mailboxEventMatchesWait(ev, matchOpts) {
				return *ev.Message, nil
			}
		case <-timer.C:
			return proto.MailboxMessageSummary{}, fmt.Errorf("timeout waiting for message for %s", opts.PaneRef)
		case <-ctx.context().Done():
			return proto.MailboxMessageSummary{}, ctx.context().Err()
		}
	}
}

func cmdCursor(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.Cursor(waitCommandContext{ctx}, ctx.Args))
}

func cmdWait(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.Wait(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func cmdLayoutJSON(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.LayoutJSON(waitCommandContext{ctx}, ctx.Args))
}

func cmdWaitFor(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.WaitFor(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func cmdWaitBusy(ctx *CommandContext) {
	ctx.applyCommandResult(waitcmd.WaitBusy(waitCommandContext{ctx}, ctx.ActorPaneID, ctx.Args))
}

func waitForPaneBusy(ctx context.Context, sess *Session, paneID uint32, paneRef string, timeout time.Duration) error {
	checkBusy := func() (bool, error) {
		pane, err := enqueueSessionQueryOnState(ctx, sess, func(sess *Session) (*mux.Pane, error) {
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
		pane, err := enqueueSessionQueryOnState(ctx, sess, func(sess *Session) (*mux.Pane, error) {
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

	outputCh := sess.enqueuePaneOutputSubscribe(ctx, paneID)
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
		candidateProcessGroup := waitcmd.WaitBusyForegroundProcessGroup(st)
		if candidateProcessGroup != 0 {
			time.Sleep(50 * time.Millisecond)
			st2, err := checkBusyStatus()
			if err != nil {
				return err
			}
			if _, ready := waitcmd.WaitBusyReady(candidateProcessGroup, st2); ready {
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
		case <-ctx.Done():
			return ctx.Err()
		}

		st, err := checkBusyStatus()
		if err != nil {
			return err
		}
		nextProcessGroup, ready := waitcmd.WaitBusyReady(candidateProcessGroup, st)
		if ready {
			return nil
		}
		candidateProcessGroup = nextProcessGroup
	}
}

func waitForUIEvent(ctx context.Context, sess *Session, requestedClientID, eventName string, afterGen uint64, afterSet bool, timeout time.Duration) (string, error) {
	if !proto.IsKnownUIEvent(eventName) {
		return "", errUnknownUIEvent(eventName)
	}

	subscription, err := sess.enqueueUIWaitSubscribe(ctx, requestedClientID, eventName)
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
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func waitForNextUIEventContext(ctx context.Context, sess *Session, client uiClientSnapshot, eventName string, timeout time.Duration) error {
	_, err := waitForUIEvent(ctx, sess, client.clientID, eventName, client.currentGen, true, timeout)
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
