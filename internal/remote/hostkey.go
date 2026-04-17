package remote

import (
	charmlog "github.com/charmbracelet/log"
	transportssh "github.com/weill-labs/amux/internal/transport/ssh"
	"golang.org/x/crypto/ssh"
)

func hostKeyCallback(knownHostsPath string, loggers ...*charmlog.Logger) ssh.HostKeyCallback {
	return transportssh.HostKeyCallback(knownHostsPath, loggers...)
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	return transportssh.AppendKnownHost(path, hostname, key)
}

func defaultKnownHostsPath() (string, error) {
	return transportssh.DefaultKnownHostsPath()
}
