package server

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
	remotecmd "github.com/weill-labs/amux/internal/server/commands/remote"
)

const ReloadServerExecPathFlag = remotecmd.ReloadServerExecPathFlag

func requestedReloadExecPath(args []string) (string, error) {
	return remotecmd.RequestedReloadExecPath(args)
}

func cmdHosts(ctx *CommandContext) {
	if ctx.Sess.RemoteManager == nil {
		ctx.reply("No remote hosts configured.\n")
		return
	}
	statuses := ctx.Sess.RemoteManager.AllHostStatus()
	if len(statuses) == 0 {
		ctx.reply("No remote hosts configured.\n")
		return
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-20s %-15s\n", "HOST", "STATUS"))
	for name, state := range statuses {
		output.WriteString(fmt.Sprintf("%-20s %-15s\n", name, state))
	}
	ctx.reply(output.String())
}

func cmdDisconnect(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: disconnect <host>")
		return
	}
	if ctx.Sess.RemoteManager == nil {
		ctx.replyErr("no remote hosts configured")
		return
	}
	if err := ctx.Sess.RemoteManager.DisconnectHost(ctx.Args[0]); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Disconnected from %s\n", ctx.Args[0]))
}

func cmdReconnect(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: reconnect <host>")
		return
	}
	if ctx.Sess.RemoteManager == nil {
		ctx.replyErr("no remote hosts configured")
		return
	}
	if err := ctx.Sess.RemoteManager.ReconnectHost(ctx.Args[0], ctx.Sess.Name); err != nil {
		ctx.replyErr(err.Error())
		return
	}
	ctx.reply(fmt.Sprintf("Reconnected to %s\n", ctx.Args[0]))
}

func cmdReloadServer(ctx *CommandContext) {
	execPath, err := requestedReloadExecPath(ctx.Args)
	if err != nil {
		ctx.replyErr(fmt.Sprintf("reload: %v", err))
		return
	}
	if execPath == "" {
		resolve := ctx.Srv.ResolveReloadExecPath
		if resolve == nil {
			resolve = reload.ResolveExecutable
		}
		execPath, err = resolve()
		if err != nil {
			ctx.replyErr(fmt.Sprintf("reload: %v", err))
			return
		}
	}
	ctx.reply("Server reloading...\n")
	if err := ctx.Srv.Reload(execPath); err != nil {
		ctx.replyErr(err.Error())
	}
}

func cmdUnsplice(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: unsplice <host>")
		return
	}
	hostName := ctx.Args[0]

	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no active window")}
		}

		var proxyIDs []uint32
		for _, p := range sess.Panes {
			if p.Meta.Host == hostName && p.IsProxy() {
				proxyIDs = append(proxyIDs, p.ID)
			}
		}
		if len(proxyIDs) == 0 {
			return commandMutationResult{err: fmt.Errorf("no spliced panes for host %q", hostName)}
		}

		var cellW, cellH int
		for _, p := range sess.Panes {
			if p.Meta.Host == hostName && p.IsProxy() {
				if c := w.Root.FindPane(p.ID); c != nil && c.Parent != nil {
					cellW, cellH = c.Parent.W, c.Parent.H
					break
				}
			}
		}
		if cellW == 0 {
			cellW, cellH = w.Width, w.Height
		}

		pane, err := sess.createPane(ctx.Srv, cellW, mux.PaneContentHeight(cellH))
		if err != nil {
			return commandMutationResult{err: err}
		}
		if err := w.UnsplicePane(hostName, pane); err != nil {
			return cleanupFailedPaneMutation(sess, pane, err)
		}

		for _, id := range proxyIDs {
			sess.removePane(id)
		}

		return commandMutationResult{
			output:          fmt.Sprintf("Unspliced %s: %d proxy panes removed\n", hostName, len(proxyIDs)),
			broadcastLayout: true,
			startPanes:      []*mux.Pane{pane},
		}
	}))
}

func cmdInjectProxy(ctx *CommandContext) {
	if len(ctx.Args) < 1 {
		ctx.replyErr("usage: _inject-proxy <host>")
		return
	}
	hostName := ctx.Args[0]
	ctx.replyCommandMutation(ctx.Sess.enqueueCommandMutation(func(sess *Session) commandMutationResult {
		w := sess.activeWindow()
		if w == nil {
			return commandMutationResult{err: fmt.Errorf("no window")}
		}
		id := sess.counter.Add(1)
		meta := mux.PaneMeta{
			Name:  fmt.Sprintf(mux.PaneNameFormat, id),
			Host:  hostName,
			Color: config.AccentColor(0),
		}
		proxyPane := mux.NewProxyPaneWithScrollback(id, meta, w.Width/2, mux.PaneContentHeight(w.Height), sess.scrollbackLines,
			sess.paneOutputCallback(),
			sess.paneExitCallback(),
			func(data []byte) (int, error) { return len(data), nil },
		)
		sess.Panes = append(sess.Panes, proxyPane)
		if _, err := w.Split(mux.SplitVertical, proxyPane); err != nil {
			sess.removePane(proxyPane.ID)
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Injected proxy pane %s @%s\n", meta.Name, hostName),
			broadcastLayout: true,
		}
	}))
}
