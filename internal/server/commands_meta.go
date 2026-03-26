package server

import (
	"fmt"
	"slices"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
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

func cmdIssue(ctx *CommandContext) {
	paneRef, issue, err := parseIssueArgs(ctx.Args)
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, err := resolveIssuePane(sess, ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		if !slices.Contains(pane.Meta.Issues, issue) {
			pane.Meta.Issues = append(pane.Meta.Issues, issue)
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func parseIssueArgs(args []string) (paneRef, issue string, err error) {
	switch len(args) {
	case 1:
		issue, err = metacmd.ParseIssue(args[0])
	case 2:
		paneRef = args[0]
		issue, err = metacmd.ParseIssue(args[1])
	default:
		return "", "", fmt.Errorf("usage: issue [pane] <issue>")
	}
	if err != nil {
		return "", "", err
	}
	return paneRef, issue, nil
}

func resolveIssuePane(sess *Session, actorPaneID uint32, paneRef string) (*mux.Pane, error) {
	if paneRef != "" {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(actorPaneID, paneRef)
		if err != nil {
			return nil, err
		}
		return pane, nil
	}
	if actorPaneID == 0 {
		return nil, fmt.Errorf("amux issue: run inside an amux pane or pass <pane> explicitly")
	}
	pane := sess.findPaneByID(actorPaneID)
	if pane == nil {
		return nil, fmt.Errorf("actor pane %d not found", actorPaneID)
	}
	return pane, nil
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
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
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
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
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
