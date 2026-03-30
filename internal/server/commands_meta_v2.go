package server

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

func cmdMetaGet(ctx *CommandContext, args []string) {
	if len(args) < 1 {
		ctx.replyErr("usage: meta get <pane> [key]")
		return
	}
	paneRef := args[0]
	requested := args[1:]

	result, err := enqueueSessionQuery(ctx.Sess, func(sess *Session) (string, error) {
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
			if err := removePaneKVValue(pane, key); err != nil {
				return commandMutationResult{err: err}
			}
		}
		return commandMutationResult{broadcastLayout: true}
	}))
}
