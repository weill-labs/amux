package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/checkpoint"
	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/remote"
)

type remotePaneAddress struct {
	host string
	pane string
}

type remotePaneCommandTarget struct {
	ref    checkpoint.RemoteRef
	host   config.Host
	dialer remote.Dialer
	paneID uint32
}

func parseRemotePaneAddress(raw string) (remotePaneAddress, bool, error) {
	host, pane, ok := strings.Cut(raw, ":")
	if !ok {
		return remotePaneAddress{}, false, nil
	}
	host = strings.TrimSpace(host)
	pane = strings.TrimSpace(pane)
	if host == "" || pane == "" {
		return remotePaneAddress{}, true, fmt.Errorf("remote pane target must be <host>:<pane>")
	}
	return remotePaneAddress{host: host, pane: pane}, true, nil
}

func resolveRemotePaneCommandTarget(ctx *CommandContext, addr remotePaneAddress) (remotePaneCommandTarget, error) {
	target, err := remotePaneCommandTargetForRef(ctx, checkpoint.RemoteRef{
		Host:     addr.host,
		PaneName: addr.pane,
	})
	if err != nil {
		return remotePaneCommandTarget{}, err
	}
	layout, err := listRemotePanesWithDialer(commandContext(ctx), target.host, target.dialer)
	if err != nil {
		return remotePaneCommandTarget{}, err
	}
	paneID, err := remote.ResolvePaneIDFromLayout(layout, addr.pane)
	if err != nil {
		return remotePaneCommandTarget{}, err
	}
	target.paneID = paneID
	return target, nil
}

func remotePaneCommandTargetForRef(ctx *CommandContext, ref checkpoint.RemoteRef) (remotePaneCommandTarget, error) {
	host, dialer, err := remoteCommandTransport(ctx, ref)
	if err != nil {
		return remotePaneCommandTarget{}, err
	}
	if strings.TrimSpace(ref.Session) == "" {
		ref.Session = remoteCommandSession(host)
	}
	return remotePaneCommandTarget{
		ref:    ref,
		host:   host,
		dialer: dialer,
	}, nil
}

func remoteCommandTransport(ctx *CommandContext, ref checkpoint.RemoteRef) (config.Host, remote.Dialer, error) {
	if ref.Host == "" {
		return config.Host{}, nil, fmt.Errorf("remote host is required")
	}
	var (
		host   config.Host
		dialer remote.Dialer
		ok     bool
	)
	if ctx != nil && ctx.Sess != nil && ctx.Sess.mirror != nil {
		host, ok = ctx.Sess.mirror.Host(ref.Host)
		dialer = ctx.Sess.mirror.Dialer()
	}
	if !ok {
		var err error
		host, err = lookupRemoteHost(ref.Host)
		if err != nil {
			return config.Host{}, nil, err
		}
	}
	if strings.TrimSpace(host.Session) == "" {
		host.Session = ref.Session
	}
	if strings.TrimSpace(host.Session) == "" {
		host.Session = DefaultSessionName
	}
	return host, dialer, nil
}

func listRemotePanesWithDialer(parent context.Context, host config.Host, dialer remote.Dialer) (*proto.LayoutSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, remoteCommandTimeout)
	defer cancel()
	if dialer == nil {
		dialer = remote.SSHDialer{}
	}
	conn, err := dialer.Dial(ctx, host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return remote.ListPanes(ctx, conn, remoteCommandSession(host))
}

func runRemoteOneShotCommandForTarget(ctx *CommandContext, target remotePaneCommandTarget, name string, args []string) (*proto.Message, error) {
	return runRemoteOneShotCommandWithDialer(commandContext(ctx), target.host, target.dialer, name, args)
}

func commandContext(ctx *CommandContext) context.Context {
	if ctx != nil {
		return ctx.context()
	}
	return serverInternalContext()
}
