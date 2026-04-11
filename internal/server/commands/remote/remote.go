package remote

import (
	"fmt"
	"sort"
	"strings"

	"github.com/weill-labs/amux/internal/proto"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

type Context interface {
	HostStatuses() map[string]string
	DisconnectHost(host string) error
	ReconnectHost(host string) error
	ResolveReloadExecPath() (string, error)
	ReloadServer(execPath string) error
	UnspliceHost(host string) commandpkg.Result
	InjectProxy(host string) commandpkg.Result
}

func Hosts(ctx Context, _ []string) commandpkg.Result {
	statuses := ctx.HostStatuses()
	if len(statuses) == 0 {
		return commandpkg.Result{Output: "No remote hosts configured.\n"}
	}

	keys := make([]string, 0, len(statuses))
	for name := range statuses {
		keys = append(keys, name)
	}
	sort.Strings(keys)

	var output strings.Builder
	output.WriteString(fmt.Sprintf("%-20s %-15s\n", "HOST", "STATUS"))
	for _, name := range keys {
		output.WriteString(fmt.Sprintf("%-20s %-15s\n", name, statuses[name]))
	}
	return commandpkg.Result{Output: output.String()}
}

func Disconnect(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: disconnect <host>")}
	}
	host := args[0]
	if err := ctx.DisconnectHost(host); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Disconnected from %s\n", host)}
}

func Reconnect(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: reconnect <host>")}
	}
	host := args[0]
	if err := ctx.ReconnectHost(host); err != nil {
		return commandpkg.Result{Err: err}
	}
	return commandpkg.Result{Output: fmt.Sprintf("Reconnected to %s\n", host)}
}

func ReloadServer(ctx Context, args []string) commandpkg.Result {
	execPath, err := RequestedReloadExecPath(args)
	if err != nil {
		return commandpkg.Result{Err: fmt.Errorf("reload: %v", err)}
	}
	if execPath == "" {
		execPath, err = ctx.ResolveReloadExecPath()
		if err != nil {
			return commandpkg.Result{Err: fmt.Errorf("reload: %v", err)}
		}
	}
	return commandpkg.Result{
		Stream: func(sender commandpkg.StreamSender) error {
			if err := sender.Send(&proto.Message{
				Type:      proto.MsgTypeCmdResult,
				CmdOutput: "Server reloading...\n",
			}); err != nil {
				return err
			}
			// Reload may exec immediately, so wait for the queued command reply to
			// reach the client before the server replaces its process image.
			if err := sender.Flush(); err != nil {
				return err
			}
			return ctx.ReloadServer(execPath)
		},
	}
}

func Unsplice(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: unsplice <host>")}
	}
	host := args[0]
	return commandpkg.Result{
		Mutate: func() commandpkg.Result {
			return ctx.UnspliceHost(host)
		},
	}
}

func InjectProxy(ctx Context, args []string) commandpkg.Result {
	if len(args) < 1 {
		return commandpkg.Result{Err: fmt.Errorf("usage: _inject-proxy <host>")}
	}
	host := args[0]
	return commandpkg.Result{
		Mutate: func() commandpkg.Result {
			return ctx.InjectProxy(host)
		},
	}
}
