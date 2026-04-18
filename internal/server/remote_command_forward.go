package server

import (
	"fmt"

	commandpkg "github.com/weill-labs/amux/internal/server/commands"
)

func (s *Session) runRemoteCommand(hostName string, cmdName string, cmdArgs []string) (string, error) {
	if s == nil || s.RemoteManager == nil {
		return "", fmt.Errorf("no remote hosts configured")
	}
	return s.RemoteManager.RunHostCommand(hostName, managedSessionName(s.Name), cmdName, cmdArgs)
}

func remoteCommandResult(sess *Session, hostName string, cmdName string, cmdArgs []string) commandpkg.Result {
	output, err := sess.runRemoteCommand(hostName, cmdName, cmdArgs)
	return commandpkg.Result{
		Output: output,
		Err:    err,
	}
}

func rewritePaneRefArg(args []string, index int, pane string) []string {
	rewritten := append([]string(nil), args...)
	if index < 0 || index >= len(rewritten) {
		return rewritten
	}
	if pane == "" {
		return append(rewritten[:index], rewritten[index+1:]...)
	}
	rewritten[index] = pane
	return rewritten
}
