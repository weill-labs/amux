package server

import (
	"fmt"

	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/reload"
	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

// ReloadServerExecPathFlag is the CLI subcommand flag used to pass the
// executable path the running server should re-exec into.
const ReloadServerExecPathFlag = "--exec-path"

func requestedReloadExecPath(args []string) (string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] != ReloadServerExecPathFlag {
			continue
		}
		if i+1 >= len(args) {
			return "", fmt.Errorf("missing value for %s", ReloadServerExecPathFlag)
		}
		return reload.NormalizeExecutablePath(args[i+1])
	}
	return "", nil
}

func cmdReloadServer(ctx *CommandContext) {
	execPath, err := requestedReloadExecPath(ctx.Args)
	if err != nil {
		ctx.applyCommandResult(commandpkg.Result{Err: fmt.Errorf("reload: %w", err)})
		return
	}
	if execPath == "" {
		resolve := ctx.Srv.ResolveReloadExecPath
		if resolve == nil {
			resolve = reload.ResolveExecutable
		}
		execPath, err = resolve()
		if err != nil {
			ctx.applyCommandResult(commandpkg.Result{Err: fmt.Errorf("reload: %w", err)})
			return
		}
	}
	ctx.applyCommandResult(commandpkg.Result{
		Stream: func(sender commandpkg.StreamSender) error {
			if err := sender.Send(&proto.Message{
				Type:      proto.MsgTypeCmdResult,
				CmdOutput: "Server reloading...\n",
			}); err != nil {
				return err
			}
			if err := sender.Flush(); err != nil {
				return err
			}
			return ctx.Srv.Reload(execPath)
		},
	})
}
