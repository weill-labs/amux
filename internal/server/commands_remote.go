package server

import (
	"fmt"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	remotecmd "github.com/weill-labs/amux/internal/server/commands/remote"
)

const ReloadServerExecPathFlag = remotecmd.ReloadServerExecPathFlag

func requestedReloadExecPath(args []string) (string, error) {
	return remotecmd.RequestedReloadExecPath(args)
}

type remoteCommandContext struct {
	*CommandContext
}

func (ctx remoteCommandContext) HostStatuses() map[string]string {
	if ctx.Sess.RemoteManager == nil {
		return nil
	}
	statuses := ctx.Sess.RemoteManager.AllHostStatus()
	cloned := make(map[string]string, len(statuses))
	for name, state := range statuses {
		cloned[name] = string(state)
	}
	return cloned
}

func (ctx remoteCommandContext) DisconnectHost(host string) error {
	if ctx.Sess.RemoteManager == nil {
		return fmt.Errorf("no remote hosts configured")
	}
	return ctx.Sess.RemoteManager.DisconnectHost(host)
}

func (ctx remoteCommandContext) ReconnectHost(host string) error {
	if ctx.Sess.RemoteManager == nil {
		return fmt.Errorf("no remote hosts configured")
	}
	return ctx.Sess.RemoteManager.ReconnectHost(host, ctx.Sess.Name)
}

func (ctx remoteCommandContext) ResolveReloadExecPath() (string, error) {
	resolve := ctx.Srv.ResolveReloadExecPath
	if resolve == nil {
		resolve = reload.ResolveExecutable
	}
	return resolve()
}

func (ctx remoteCommandContext) ReloadServer(execPath string) error {
	return ctx.Srv.Reload(execPath)
}

func (ctx remoteCommandContext) UnspliceHost(hostName string) commandpkg.Result {
	w := ctx.Sess.activeWindow()
	if w == nil {
		return commandpkg.Result{Err: fmt.Errorf("no active window")}
	}

	var proxyIDs []uint32
	for _, p := range ctx.Sess.Panes {
		if p.Meta.Host == hostName && p.IsProxy() {
			proxyIDs = append(proxyIDs, p.ID)
		}
	}
	if len(proxyIDs) == 0 {
		return commandpkg.Result{Err: fmt.Errorf("no spliced panes for host %q", hostName)}
	}

	var cellW, cellH int
	for _, p := range ctx.Sess.Panes {
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

	pane, err := ctx.Sess.createPane(ctx.Srv, cellW, mux.PaneContentHeight(cellH))
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if err := w.UnsplicePane(hostName, pane); err != nil {
		return toCommandResult(cleanupFailedPaneMutation(ctx.Sess, pane, err))
	}

	for _, id := range proxyIDs {
		ctx.Sess.removePane(id)
	}

	return commandpkg.Result{
		Output:          fmt.Sprintf("Unspliced %s: %d proxy panes removed\n", hostName, len(proxyIDs)),
		BroadcastLayout: true,
		StartPanes:      []*mux.Pane{pane},
	}
}

func (ctx remoteCommandContext) InjectProxy(hostName string) commandpkg.Result {
	w := ctx.Sess.activeWindow()
	if w == nil {
		return commandpkg.Result{Err: fmt.Errorf("no window")}
	}
	id := ctx.Sess.counter.Add(1)
	meta := mux.PaneMeta{
		Name:  fmt.Sprintf(mux.PaneNameFormat, id),
		Host:  hostName,
		Color: config.AccentColor(0),
	}
	proxyPane := ctx.Sess.ownPane(mux.NewProxyPaneWithScrollback(id, meta, w.Width/2, mux.PaneContentHeight(w.Height), ctx.Sess.scrollbackLines,
		ctx.Sess.paneOutputCallback(),
		ctx.Sess.paneExitCallback(),
		func(data []byte) (int, error) { return len(data), nil },
	))
	ctx.Sess.Panes = append(ctx.Sess.Panes, proxyPane)
	if _, err := w.Split(mux.SplitVertical, proxyPane); err != nil {
		ctx.Sess.removePane(proxyPane.ID)
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{
		Output:          fmt.Sprintf("Injected proxy pane %s @%s\n", meta.Name, hostName),
		BroadcastLayout: true,
	}
}

func cmdHosts(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.Hosts(remoteCommandContext{ctx}, ctx.Args))
}

func cmdDisconnect(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.Disconnect(remoteCommandContext{ctx}, ctx.Args))
}

func cmdReconnect(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.Reconnect(remoteCommandContext{ctx}, ctx.Args))
}

func cmdReloadServer(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.ReloadServer(remoteCommandContext{ctx}, ctx.Args))
}

func cmdUnsplice(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.Unsplice(remoteCommandContext{ctx}, ctx.Args))
}

func cmdInjectProxy(ctx *CommandContext) {
	ctx.applyCommandResult(remotecmd.InjectProxy(remoteCommandContext{ctx}, ctx.Args))
}
