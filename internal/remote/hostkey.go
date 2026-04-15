package remote

import (
	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/sshutil"
	"golang.org/x/crypto/ssh"
)

func hostKeyCallback(knownHostsPath string, loggers ...*charmlog.Logger) ssh.HostKeyCallback {
	return sshutil.HostKeyCallback(knownHostsPath, loggers...)
}

func appendKnownHost(path, hostname string, key ssh.PublicKey) error {
	return sshutil.AppendKnownHost(path, hostname, key)
}

func defaultKnownHostsPath() (string, error) {
	return sshutil.DefaultKnownHostsPath()
}
