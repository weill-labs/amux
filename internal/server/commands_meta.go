package server

import (
	"fmt"
	"strings"

	metacmd "github.com/weill-labs/amux/internal/server/commands/meta"
)

func cmdSetMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: set-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	kvPairs := ctx.Args[1:]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, kv := range kvPairs {
			key, value, ok := strings.Cut(kv, "=")
			if !ok {
				return commandMutationResult{err: fmt.Errorf("invalid key=value: %q", kv)}
			}
			switch key {
			case "task":
				pane.Meta.Task = value
			case "pr":
				pane.Meta.PR = value
			case "branch":
				if value == "" {
					pane.SetMetaManualBranch(false)
					pane.Meta.GitBranch = ""
				} else {
					pane.Meta.GitBranch = value
					pane.SetMetaManualBranch(true)
				}
			default:
				return commandMutationResult{err: fmt.Errorf("unknown meta key: %q (valid: task, pr, branch)", key)}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdAddMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: add-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	update, err := metacmd.ParseCollectionArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	res := ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.PRs {
			pane.Meta.TrackedPRs = metacmd.UpsertTrackedPR(pane.Meta.TrackedPRs, pr)
		}
		for _, issue := range update.Issues {
			pane.Meta.TrackedIssues = metacmd.UpsertTrackedIssue(pane.Meta.TrackedIssues, issue)
		}
		return commandMutationResult{broadcastLayout: true}
	})
	if res.err == nil {
		if err := ctx.Sess.refreshTrackedMetaForPaneRef(ctx.ActorPaneID, paneRef); err != nil {
			res.err = err
		}
	}
	if res.err == nil {
		ctx.Sess.transitionTrackedIssuesToStartedAsync(update.Issues)
	}
	ctx.replyCommandMutation(res)
}

func cmdRmMeta(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: rm-meta <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	update, err := metacmd.ParseCollectionArgs(ctx.Args[1:])
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.PRs {
			pane.Meta.TrackedPRs = metacmd.RemoveTrackedPR(pane.Meta.TrackedPRs, pr)
		}
		for _, issue := range update.Issues {
			pane.Meta.TrackedIssues = metacmd.RemoveTrackedIssue(pane.Meta.TrackedIssues, issue)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdRefreshMeta(ctx *CommandContext) {
	if len(ctx.Args) > 1 {
		ctx.replyErr("usage: refresh-meta [pane]")
		return
	}

	paneRef := ""
	if len(ctx.Args) == 1 {
		paneRef = ctx.Args[0]
	}

	if err := ctx.Sess.refreshTrackedMetaForPaneRef(ctx.ActorPaneID, paneRef); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply("")
}
