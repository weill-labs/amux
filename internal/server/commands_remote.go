package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/mux"
	"github.com/weill-labs/amux/internal/reload"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
	remotecmd "github.com/weill-labs/amux/internal/server/commands/remote"
)

const ReloadServerExecPathFlag = remotecmd.ReloadServerExecPathFlag

const connectCommandUsage = "usage: connect <host> [--session <name> | --session-per-client]"

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

func (ctx remoteCommandContext) FinalizeDisconnect(hostName string) commandpkg.Result {
	return commandpkg.Result{
		Mutate: func() commandpkg.Result {
			if err := ctx.Sess.disconnectRemoteSession(hostName); err != nil {
				return commandpkg.Result{Err: err}
			}
			return commandpkg.Result{
				Output:          fmt.Sprintf("Disconnected from %s\n", hostName),
				BroadcastLayout: true,
			}
		},
	}
}

func (ctx remoteCommandContext) ReconnectHost(host string) error {
	if ctx.Sess.RemoteManager == nil {
		return fmt.Errorf("no remote hosts configured")
	}
	return ctx.Sess.RemoteManager.ReconnectHost(host, managedSessionName(ctx.Sess.Name))
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

type connectTarget struct {
	hostName    string
	sessionName string
}

func parseConnectTarget(args []string, localSessionName string) (connectTarget, error) {
	target := connectTarget{sessionName: DefaultSessionName}
	sessionExplicit := false
	perClient := false

	for i := 0; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--session":
			if perClient || i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "-") {
				return connectTarget{}, errors.New(connectCommandUsage)
			}
			target.sessionName = args[i+1]
			sessionExplicit = true
			i++
		case "--session-per-client":
			if sessionExplicit || perClient {
				return connectTarget{}, errors.New(connectCommandUsage)
			}
			perClient = true
		default:
			if strings.HasPrefix(arg, "-") {
				return connectTarget{}, errors.New(connectCommandUsage)
			}
			if target.hostName != "" {
				return connectTarget{}, errors.New(connectCommandUsage)
			}
			target.hostName = arg
		}
	}

	if target.hostName == "" {
		return connectTarget{}, errors.New(connectCommandUsage)
	}
	if perClient {
		target.sessionName = managedSessionName(localSessionName)
	}
	return target, nil
}

func runConnect(ctx *CommandContext) commandpkg.Result {
	target, err := parseConnectTarget(ctx.Args, ctx.Sess.Name)
	if err != nil {
		return commandpkg.Result{Err: err}
	}
	if ctx.Sess.RemoteManager == nil {
		return commandpkg.Result{Err: fmt.Errorf("no remote hosts configured")}
	}

	hostName := target.hostName
	layout, err := ctx.Sess.RemoteManager.ConnectHost(hostName, target.sessionName)
	if err != nil {
		return commandpkg.Result{Err: err}
	}

	return toCommandResult(ctx.Sess.enqueueCommandMutation(func(mctx *MutationContext) commandMutationResult {
		if err := mutationContextDo(mctx, func(sess *Session) error {
			return sess.connectRemoteSession(hostName, layout, RemoteSessionConnect, 0, true)
		}); err != nil {
			return commandMutationResult{err: err}
		}
		return commandMutationResult{
			output:          fmt.Sprintf("Connected to %s\n", hostName),
			broadcastLayout: true,
		}
	}))
}

func cmdConnect(ctx *CommandContext) {
	ctx.applyCommandResult(runConnect(ctx))
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
