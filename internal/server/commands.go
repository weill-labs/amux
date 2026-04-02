package server

import (
	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

// CommandHandler processes a single CLI command.
type CommandHandler func(ctx *CommandContext)

// CommandContext provides all state a command handler needs.
type CommandContext struct {
	CommandName string
	CC          *clientConn
	Srv         *Server
	Sess        *Session
	Args        []string
	ActorPaneID uint32
	auditErr    string
}

func (ctx *CommandContext) reply(output string) {
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: output})
}

func (ctx *CommandContext) replyErr(errMsg string) {
	ctx.auditErr = errMsg
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdErr: errMsg})
}

func (ctx *CommandContext) replyCommandMutation(res commandMutationResult) {
	for _, pane := range res.closePanes {
		ctx.Sess.closePaneAsync(pane)
	}
	if res.err != nil {
		ctx.replyErr(res.err.Error())
		return
	}
	for _, pane := range res.startPanes {
		pane.Start()
	}
	if res.bell {
		ctx.CC.Send(&Message{Type: MsgTypeBell})
	}
	if res.output != "" {
		ctx.reply(res.output)
	} else {
		ctx.CC.Send(&Message{Type: MsgTypeCmdResult})
	}
	if res.sendExit {
		ctx.Sess.broadcast(&Message{Type: MsgTypeExit})
	}
	if res.shutdownServer {
		go ctx.Srv.Shutdown()
	}
}

type commandStreamSender struct {
	cc *clientConn
}

func (s commandStreamSender) Send(msg *proto.Message) error {
	return s.cc.Send(msg)
}

func (ctx *CommandContext) applyCommandResult(res commandpkg.Result) {
	switch {
	case res.Mutate != nil:
		ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
			return toCommandMutationResult(res.Mutate())
		}))
	case res.Stream != nil:
		if err := res.Stream(commandStreamSender{cc: ctx.CC}); err != nil {
			ctx.replyErr(err.Error())
		}
	case res.Message != nil:
		if err := ctx.CC.Send(res.Message); err != nil {
			return
		}
	default:
		ctx.replyCommandMutation(toCommandMutationResult(res))
	}
}

func toCommandMutationResult(res commandpkg.Result) commandMutationResult {
	histories := make([]paneHistoryUpdate, 0, len(res.PaneHistories))
	for _, update := range res.PaneHistories {
		histories = append(histories, paneHistoryUpdate{
			paneID:  update.PaneID,
			history: update.History,
		})
	}
	renders := make([]paneRender, 0, len(res.PaneRenders))
	for _, render := range res.PaneRenders {
		renders = append(renders, paneRender{
			paneID: render.PaneID,
			data:   render.Data,
		})
	}
	return commandMutationResult{
		output:          res.Output,
		err:             res.Err,
		broadcastLayout: res.BroadcastLayout,
		paneHistories:   histories,
		paneRenders:     renders,
		startPanes:      res.StartPanes,
		closePanes:      res.ClosePanes,
		sendExit:        res.SendExit,
		shutdownServer:  res.ShutdownServer,
	}
}

func toCommandResult(res commandMutationResult) commandpkg.Result {
	histories := make([]commandpkg.PaneHistoryUpdate, 0, len(res.paneHistories))
	for _, update := range res.paneHistories {
		histories = append(histories, commandpkg.PaneHistoryUpdate{
			PaneID:  update.paneID,
			History: update.history,
		})
	}
	renders := make([]commandpkg.PaneRender, 0, len(res.paneRenders))
	for _, render := range res.paneRenders {
		renders = append(renders, commandpkg.PaneRender{
			PaneID: render.paneID,
			Data:   render.data,
		})
	}
	return commandpkg.Result{
		Output:          res.output,
		Err:             res.err,
		BroadcastLayout: res.broadcastLayout,
		PaneHistories:   histories,
		PaneRenders:     renders,
		StartPanes:      res.startPanes,
		ClosePanes:      res.closePanes,
		SendExit:        res.sendExit,
		ShutdownServer:  res.shutdownServer,
	}
}

func (ctx *CommandContext) activeWindowSnapshot() (activePid, width, height int, err error) {
	snap, err := ctx.Sess.queryActiveWindowSnapshot()
	if err != nil {
		return 0, 0, 0, err
	}
	return snap.activePID, snap.width, snap.height, nil
}

// commandRegistry maps command names to their handlers, following
// tmux's pattern of one entry per command.
var commandRegistry = map[string]CommandHandler{
	"list":           cmdList,
	"split":          cmdSplit,
	"focus":          cmdFocus,
	"capture":        cmdCapture,
	"spawn":          cmdSpawn,
	"zoom":           cmdZoom,
	"reset":          cmdReset,
	"respawn":        cmdRespawn,
	"kill":           cmdKill,
	"undo":           cmdUndo,
	"send-keys":      cmdSendKeys,
	"broadcast":      cmdBroadcast,
	"status":         cmdStatus,
	"new-window":     cmdNewWindow,
	"list-windows":   cmdListWindows,
	"list-clients":   cmdListClients,
	"connection-log": cmdConnectionLog,
	"pane-log":       cmdPaneLog,
	"select-window":  cmdSelectWindow,
	"next-window":    cmdNextWindow,
	"prev-window":    cmdPrevWindow,
	"last-window":    cmdLastWindow,
	"rename-window":  cmdRenameWindow,
	"resize-border":  cmdResizeBorder,
	"resize-active":  cmdResizeActive,
	"resize-pane":    cmdResizePane,
	"equalize":       cmdEqualize,
	"resize-window":  cmdResizeWindow,
	"swap":           cmdSwap,
	"swap-tree":      cmdSwapTree,
	"move":           cmdMove,
	"move-up":        cmdMoveUp,
	"move-down":      cmdMoveDown,
	"move-to":        cmdMoveTo,
	"drop-pane":      cmdDropPane,
	"rotate":         cmdRotate,
	"copy-mode":      cmdCopyMode,
	"cursor":         cmdCursor,
	"meta":           cmdMeta,
	"wait":           cmdWait,
	"_layout-json":   cmdLayoutJSON,
	"events":         cmdEvents,
	"hosts":          cmdHosts,
	"disconnect":     cmdDisconnect,
	"reconnect":      cmdReconnect,
	"reload-server":  cmdReloadServer,
	"unsplice":       cmdUnsplice,
	"_inject-proxy":  cmdInjectProxy,
	"type-keys":      cmdTypeKeys,
	"set-kv":         cmdSetKV,
	"get-kv":         cmdGetKV,
	"rm-kv":          cmdRmKV,
	"set-lead":       cmdSetLead,
	"unset-lead":     cmdUnsetLead,
	"toggle-lead":    cmdToggleLead,
}
