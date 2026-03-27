package server

// CommandHandler processes a single CLI command.
type CommandHandler func(ctx *CommandContext)

// CommandContext provides all state a command handler needs.
type CommandContext struct {
	CC          *clientConn
	Srv         *Server
	Sess        *Session
	Args        []string
	ActorPaneID uint32
}

func (ctx *CommandContext) reply(output string) {
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdOutput: output})
}

func (ctx *CommandContext) replyErr(errMsg string) {
	ctx.CC.Send(&Message{Type: MsgTypeCmdResult, CmdErr: errMsg})
}

func (ctx *CommandContext) replyCommandMutation(res commandMutationResult) {
	if res.err != nil {
		ctx.replyErr(res.err.Error())
		return
	}
	for _, pane := range res.startPanes {
		pane.Start()
	}
	for _, pane := range res.closePanes {
		pane.Close()
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
	"split-focus":    cmdSplitFocus,
	"add-pane":       cmdAddPane,
	"focus":          cmdFocus,
	"capture":        cmdCapture,
	"spawn":          cmdSpawn,
	"spawn-focus":    cmdSpawnFocus,
	"zoom":           cmdZoom,
	"reset":          cmdReset,
	"kill":           cmdKill,
	"undo":           cmdUndo,
	"send-keys":      cmdSendKeys,
	"delegate":       cmdDelegate,
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
	"rename-window":  cmdRenameWindow,
	"resize-border":  cmdResizeBorder,
	"resize-active":  cmdResizeActive,
	"resize-pane":    cmdResizePane,
	"resize-window":  cmdResizeWindow,
	"swap":           cmdSwap,
	"swap-tree":      cmdSwapTree,
	"move":           cmdMove,
	"move-to":        cmdMoveTo,
	"rotate":         cmdRotate,
	"copy-mode":      cmdCopyMode,
	"cursor":         cmdCursor,
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
	"set-meta":       cmdSetMeta,
	"add-meta":       cmdAddMeta,
	"rm-meta":        cmdRmMeta,
	"set-lead":       cmdSetLead,
	"unset-lead":     cmdUnsetLead,
	"toggle-lead":    cmdToggleLead,
	"refresh-meta":   cmdRefreshMeta,
}
