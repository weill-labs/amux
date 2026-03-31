package server

import (
	"github.com/weill-labs/amux/internal/mux"
	metacmd "github.com/weill-labs/amux/internal/server/commands/meta"
)

type metaCommandContext struct {
	*CommandContext
}

func (ctx metaCommandContext) ResolvePaneForMutation(paneRef string) (*mux.Pane, error) {
	pane, _, err := ctx.Sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
	if err != nil {
		return nil, err
	}
	return pane, nil
}

func (ctx metaCommandContext) QueryPaneKV(paneRef string, requested []string) (string, error) {
	return enqueueSessionQuery(ctx.Sess, func(sess *Session) (string, error) {
		pane, _, err := sess.resolvePaneAcrossWindowsForActor(ctx.ActorPaneID, paneRef)
		if err != nil {
			return "", err
		}
		return metacmd.FormatPaneKV(pane.Meta, requested), nil
	})
}

func cmdMeta(ctx *CommandContext) {
	ctx.applyCommandResult(metacmd.Meta(metaCommandContext{CommandContext: ctx}, ctx.Args))
}
