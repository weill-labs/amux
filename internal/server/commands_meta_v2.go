package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/mux"
)

const metaUsage = "usage: meta <set|get|rm> ..."

func cmdMeta(ctx *CommandContext) {
	if len(ctx.Args) == 0 {
		ctx.replyErr(metaUsage)
		return
	}

	switch ctx.Args[0] {
	case "set":
		cmdMetaSet(ctx, ctx.Args[1:])
	case "get":
		cmdMetaGet(ctx, ctx.Args[1:])
	case "rm":
		cmdMetaRm(ctx, ctx.Args[1:])
	default:
		ctx.replyErr(metaUsage)
	}
}

func cmdMetaSet(ctx *CommandContext, args []string) {
	if len(args) < 2 {
		ctx.replyErr("usage: meta set <pane> key=value [key=value...]")
		return
	}
	paneRef := args[0]
	kvPairs := args[1:]

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
			if err := setPaneMetaKV(pane, key, value); err != nil {
				return commandMutationResult{err: err}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func cmdMetaGet(ctx *CommandContext, args []string) {
	if len(args) < 1 || len(args) > 2 {
		ctx.replyErr("usage: meta get <pane> [key]")
		return
	}
	paneRef := args[0]
	key := ""
	if len(args) == 2 {
		key = args[1]
	}

	result, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (string, error) {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return "", err
		}
		kv := mux.NormalizedMetaKV(pane.Meta)
		if key != "" {
			value, ok := kv[key]
			if !ok {
				return "", fmt.Errorf("meta: key %q is not set", key)
			}
			return value + "\n", nil
		}
		if len(kv) == 0 {
			return "", nil
		}
		keys := make([]string, 0, len(kv))
		for name := range kv {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		var out strings.Builder
		for _, name := range keys {
			fmt.Fprintf(&out, "%s=%s\n", name, kv[name])
		}
		return out.String(), nil
	})
	if err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(result)
}

func cmdMetaRm(ctx *CommandContext, args []string) {
	if len(args) < 2 {
		ctx.replyErr("usage: meta rm <pane> key [key...]")
		return
	}
	paneRef := args[0]
	keys := args[1:]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return commandMutationResult{err: err}
		}
		for _, key := range keys {
			if err := removePaneMetaKV(pane, key); err != nil {
				return commandMutationResult{err: err}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}

func setPaneMetaKV(pane *mux.Pane, key, value string) error {
	if key == "" {
		return fmt.Errorf("invalid meta key: %q", key)
	}
	switch key {
	case "task":
		pane.Meta.Task = value
		return nil
	case "branch":
		if value == "" {
			pane.SetMetaManualBranch(false)
			pane.Meta.GitBranch = ""
			return nil
		}
		pane.Meta.GitBranch = value
		pane.SetMetaManualBranch(true)
		return nil
	case "pr":
		pane.Meta.PR = value
		return nil
	default:
		if pane.Meta.KV == nil {
			pane.Meta.KV = make(map[string]string)
		}
		pane.Meta.KV[key] = value
		return nil
	}
}

func removePaneMetaKV(pane *mux.Pane, key string) error {
	if key == "" {
		return fmt.Errorf("invalid meta key: %q", key)
	}
	switch key {
	case "task":
		pane.Meta.Task = ""
	case "branch":
		pane.SetMetaManualBranch(false)
		pane.Meta.GitBranch = ""
	case "pr":
		pane.Meta.PR = ""
	default:
		delete(pane.Meta.KV, key)
		if len(pane.Meta.KV) == 0 {
			pane.Meta.KV = nil
		}
	}
	return nil
}
