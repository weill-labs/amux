package cli

import (
	"fmt"
	"strings"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
	transportssh "github.com/weill-labs/amux/internal/transport/ssh"
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

func resolveCLISSHTarget(raw string) (transport.Target, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return transport.Target{}, fmt.Errorf("loading config: %w", err)
	}

	target, err := transportssh.ParseTarget(raw, "")
	if err != nil {
		return transport.Target{}, err
	}
	if !strings.Contains(raw, "@") {
		target.User = cfg.HostUser(target.Host)
	}
	return target.TransportTarget(), nil
}
