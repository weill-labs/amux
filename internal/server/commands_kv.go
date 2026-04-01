package server

import (
	"github.com/weill-labs/amux/internal/mux"
	metacmd "github.com/weill-labs/amux/internal/server/commands/meta"
)

func setPaneKVValue(pane *mux.Pane, key, value string) error {
	return metacmd.SetPaneKVValue(pane, key, value)
}

func removePaneKVValue(pane *mux.Pane, key string) error {
	return metacmd.RemovePaneKVValue(pane, key)
}

func cmdSetKV(ctx *CommandContext) {
	ctx.applyCommandResult(metacmd.SetKV(metaCommandContext{CommandContext: ctx}, ctx.Args))
}

func cmdGetKV(ctx *CommandContext) {
	ctx.applyCommandResult(metacmd.GetKV(metaCommandContext{CommandContext: ctx}, ctx.Args))
}

func cmdRmKV(ctx *CommandContext) {
	ctx.applyCommandResult(metacmd.RmKV(metaCommandContext{CommandContext: ctx}, ctx.Args))
}
