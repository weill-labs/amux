package cli

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/sshutil"
)

func sshCLICommands() map[string]commandHandler {
	return map[string]commandHandler{
		"ssh": func(inv invocation, args []string) int {
			if len(args) != 1 {
				fmt.Fprintln(inv.runtime.Stderr, sshUsage)
				return 1
			}

			target, err := resolveCLISSHTarget(args[0])
			if err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux: %v\n", err)
				return 1
			}
			if err := inv.runtime.RunSSHSession(target); err != nil {
				fmt.Fprintf(inv.runtime.Stderr, "amux: %v\n", err)
				return 1
			}
			return 0
		},
	}
}

func resolveCLISSHTarget(raw string) (sshutil.SSHTarget, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return sshutil.SSHTarget{}, fmt.Errorf("loading config: %w", err)
	}

	target, err := sshutil.ParseTarget(raw, "")
	if err != nil {
		return sshutil.SSHTarget{}, err
	}
	if !strings.Contains(raw, "@") {
		target.User = cfg.HostUser(target.Host)
	}
	return target, nil
}
