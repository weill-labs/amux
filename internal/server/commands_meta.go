package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
	metacmd "github.com/weill-labs/amux/internal/server/commands/meta"
)

func parseKVArg(raw string) (key, value string, err error) {
	key, value, ok := strings.Cut(raw, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid key=value: %q", raw)
	}
	if strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("invalid key=value: %q", raw)
	}
	return key, value, nil
}

func setPaneKVValue(pane *mux.Pane, key, value string) error {
	manualBranch, err := mux.SetPaneMetaKV(&pane.Meta, key, value)
	if err != nil {
		return err
	}
	if key == mux.PaneMetaKeyBranch {
		pane.SetMetaManualBranch(manualBranch)
	}
	return nil
}

func removePaneKVValue(pane *mux.Pane, key string) error {
	manualBranch, err := mux.RemovePaneMetaKV(&pane.Meta, key)
	if err != nil {
		return err
	}
	if key == mux.PaneMetaKeyBranch {
		pane.SetMetaManualBranch(manualBranch)
	}
	return nil
}

func formatPaneKV(meta mux.PaneMeta, requested []string) string {
	kv := meta.KVSnapshot()
	if len(kv) == 0 {
		return ""
	}

	keys := requested
	if len(keys) == 0 {
		keys = make([]string, 0, len(kv))
		for key := range kv {
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}

	var out strings.Builder
	for _, key := range keys {
		value, ok := kv[key]
		if !ok {
			continue
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(value)
		out.WriteByte('\n')
	}
	return out.String()
}

func cmdSetKV(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: set-kv <pane> key=value [key=value...]")
		return
	}
	paneRef := ctx.Args[0]
	kvPairs := ctx.Args[1:]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, raw := range kvPairs {
			key, value, err := parseKVArg(raw)
			if err != nil {
				return commandMutationResult{err: err}
			}
			if err := setPaneKVValue(pane, key, value); err != nil {
				return commandMutationResult{err: err}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdGetKV(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: get-kv <pane> [key...]")
		return
	}
	paneRef := ctx.Args[0]
	requested := ctx.Args[1:]

	output, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (string, error) {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return "", err
		}
		return formatPaneKV(pane.Meta, requested), nil
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(output)
}

func cmdRmKV(ctx *CommandContext) {
	if len(ctx.Args) < 2 {
		ctx.replyErr("usage: rm-kv <pane> key [key...]")
		return
	}
	paneRef := ctx.Args[0]
	keys := ctx.Args[1:]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, key := range keys {
			if err := removePaneKVValue(pane, key); err != nil {
				return commandMutationResult{err: err}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

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
		for _, raw := range kvPairs {
			key, value, err := parseKVArg(raw)
			if err != nil {
				return commandMutationResult{err: err}
			}
			switch key {
			case mux.PaneMetaKeyTask, mux.PaneMetaKeyPR:
				if err := setPaneKVValue(pane, key, value); err != nil {
					return commandMutationResult{err: err}
				}
			case mux.PaneMetaKeyBranch:
				if value == "" {
					if err := removePaneKVValue(pane, key); err != nil {
						return commandMutationResult{err: err}
					}
				} else if err := setPaneKVValue(pane, key, value); err != nil {
					return commandMutationResult{err: err}
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
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}

		prs := pane.Meta.TrackedPRs
		for _, pr := range update.PRs {
			prs = metacmd.UpsertTrackedPR(prs, pr)
		}
		if len(prs) == 0 {
			if err := removePaneKVValue(pane, mux.PaneMetaKeyTrackedPRs); err != nil {
				return commandMutationResult{err: err}
			}
		} else if err := setPaneKVValue(pane, mux.PaneMetaKeyTrackedPRs, mux.FormatTrackedPRsValue(prs)); err != nil {
			return commandMutationResult{err: err}
		}

		issues := pane.Meta.TrackedIssues
		for _, issue := range update.Issues {
			issues = metacmd.UpsertTrackedIssue(issues, issue)
		}
		if len(issues) == 0 {
			if err := removePaneKVValue(pane, mux.PaneMetaKeyTrackedIssues); err != nil {
				return commandMutationResult{err: err}
			}
		} else if err := setPaneKVValue(pane, mux.PaneMetaKeyTrackedIssues, mux.FormatTrackedIssuesValue(issues)); err != nil {
			return commandMutationResult{err: err}
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

		prs := pane.Meta.TrackedPRs
		for _, pr := range update.PRs {
			prs = metacmd.RemoveTrackedPR(prs, pr)
		}
		if len(prs) == 0 {
			if err := removePaneKVValue(pane, mux.PaneMetaKeyTrackedPRs); err != nil {
				return commandMutationResult{err: err}
			}
		} else if err := setPaneKVValue(pane, mux.PaneMetaKeyTrackedPRs, mux.FormatTrackedPRsValue(prs)); err != nil {
			return commandMutationResult{err: err}
		}

		issues := pane.Meta.TrackedIssues
		for _, issue := range update.Issues {
			issues = metacmd.RemoveTrackedIssue(issues, issue)
		}
		if len(issues) == 0 {
			if err := removePaneKVValue(pane, mux.PaneMetaKeyTrackedIssues); err != nil {
				return commandMutationResult{err: err}
			}
		} else if err := setPaneKVValue(pane, mux.PaneMetaKeyTrackedIssues, mux.FormatTrackedIssuesValue(issues)); err != nil {
			return commandMutationResult{err: err}
		}

		return commandMutationResult{broadcastLayout: true}
	}))
}
