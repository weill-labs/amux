package server

import (
	"fmt"
	"slices"
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
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
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

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.PRs {
			if !slices.Contains(pane.Meta.PRs, pr) {
				pane.Meta.PRs = append(pane.Meta.PRs, pr)
			}
		}
		for _, issue := range update.Issues {
			if !slices.Contains(pane.Meta.Issues, issue) {
				pane.Meta.Issues = append(pane.Meta.Issues, issue)
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
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
		pane, _, err := sess.resolvePaneAcrossWindows(paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, pr := range update.PRs {
			pane.Meta.PRs = metacmd.RemoveIntValue(pane.Meta.PRs, pr)
		}
		for _, issue := range update.Issues {
			pane.Meta.Issues = metacmd.RemoveStringValue(pane.Meta.Issues, issue)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}
