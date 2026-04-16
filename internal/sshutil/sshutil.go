package sshutil

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	charmlog "github.com/charmbracelet/log"
	"github.com/weill-labs/amux/internal/auditlog"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

func BuildSSHConfig(user, identityFile string) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	keyPaths := []string{
		os.ExpandEnv("$HOME/.ssh/id_ed25519"),
		os.ExpandEnv("$HOME/.ssh/id_rsa"),
	}
	if identityFile != "" {
		keyPaths = append([]string{identityFile}, keyPaths...)
	}
	for _, keyPath := range keyPaths {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			continue
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			agentClient := agent.NewClient(conn)
			authMethods = append(authMethods, ssh.PublicKeysCallback(agentClient.Signers))
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH auth methods available (no agent, no key files)")
	}

	if user == "" {
		user = "ubuntu"
	}

	var hostKeyCallback ssh.HostKeyCallback
	if os.Getenv("AMUX_SSH_INSECURE") == "1" {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	} else {
		hostKeyCallback = HostKeyCallback("")
	}

	return &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}, nil
}

func HostKeyCallback(knownHostsPath string, loggers ...*charmlog.Logger) ssh.HostKeyCallback {
	logger := auditlog.Discard()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		path := knownHostsPath
		if path == "" {
			var err error
			path, err = DefaultKnownHostsPath()
			if err != nil {
				return fmt.Errorf("amux: cannot determine known_hosts path: %w", err)
			}
		}

		if _, err := os.Stat(path); err == nil {
			cb, err := knownhosts.New(path)
			if err != nil {
				return fmt.Errorf("amux: failed to parse %s: %w", path, err)
			}

			err = cb(hostname, remote, key)
			if err == nil {
				return nil
			}

			var revokedErr *knownhosts.RevokedError
			if errors.As(err, &revokedErr) {
				return err
			}

			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) {
				if len(keyErr.Want) > 0 {
					return hostKeyChangedError(hostname, keyErr)
				}
			} else {
				return err
			}
		}

		if err := AppendKnownHost(path, hostname, key); err != nil {
			return fmt.Errorf("amux: failed to save host key: %w", err)
		}
		logger.Info("trusted new ssh host key",
			"event", "ssh_hostkey_trust",
			"host", hostname,
			"key_type", key.Type(),
			"known_hosts", path,
		)
		return nil
	}
}

func EnsureRemoteServer(client *ssh.Client, sockPath, sessionName string) error {
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	return sess.Run(BuildEnsureServerCmd(sockPath, sessionName))
}

func DialRemoteSocket(client *ssh.Client, sockPath string) (net.Conn, error) {
	conn, err := client.Dial("unix", sockPath)
	if err == nil {
		return conn, nil
	}

	tcpConn, tcpErr := dialRemoteSocketTCP(client, sockPath)
	if tcpErr != nil {
		return nil, fmt.Errorf("unix dial failed (%w) and TCP fallback failed (%w)", err, tcpErr)
	}
	return tcpConn, nil
}

func SSHOutput(client *ssh.Client, cmd string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	out, err := sess.Output(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func RemoteSocketPath(remoteUID, sessionName string) string {
	return fmt.Sprintf("/tmp/amux-%s/%s", remoteUID, sessionName)
}

func NormalizeAddr(addr string) string {
	if !hasPort(addr) {
		return addr + ":22"
	}
	return addr
}

func AppendKnownHost(path, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintln(f, knownhosts.Line([]string{hostname}, key))
	return err
}

func DefaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

// BuildEnsureServerCmd returns the shell command that starts amux _server if
// the socket does not already exist.
func BuildEnsureServerCmd(sockPath, sessionName string) string {
	return fmt.Sprintf(
		`if [ ! -S %s ]; then AMUX=${AMUX_BIN:-$(command -v ~/.local/bin/amux 2>/dev/null || command -v amux 2>/dev/null || echo amux)}; "$AMUX" install-terminfo || exit 1; nohup "$AMUX" _server %s </dev/null >/dev/null 2>&1 & for i in 1 2 3 4 5 6 7 8 9 10; do [ -S %s ] && break; sleep 0.2; done; fi`,
		sockPath, sessionName, sockPath,
	)
}

func dialRemoteSocketTCP(client *ssh.Client, sockPath string) (net.Conn, error) {
	port, err := startSocatBridge(client, sockPath)
	if err != nil {
		return nil, err
	}

	return client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func startSocatBridge(client *ssh.Client, sockPath string) (int, error) {
	out, err := SSHOutput(client, fmt.Sprintf(
		`port=$(python3 -c "import socket; s=socket.socket(); s.bind(('',0)); print(s.getsockname()[1]); s.close()" 2>/dev/null || shuf -i 49152-65535 -n 1); `+
			`nohup socat TCP-LISTEN:$port,bind=127.0.0.1,fork,reuseaddr UNIX-CONNECT:%s </dev/null >/dev/null 2>&1 & `+
			`sleep 0.3; echo $port`, sockPath))
	if err != nil {
		return 0, fmt.Errorf("starting socat: %w", err)
	}

	var port int
	if _, err := fmt.Sscanf(out, "%d", &port); err != nil {
		return 0, fmt.Errorf("parsing socat port %q: %w", strings.TrimSpace(out), err)
	}
	if port == 0 {
		return 0, fmt.Errorf("could not parse socat port from: %s", out)
	}
	return port, nil
}

func hostKeyChangedError(hostname string, keyErr *knownhosts.KeyError) error {
	want := keyErr.Want[0]
	return fmt.Errorf(`amux: SSH host key verification failed for %s
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!    @
@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@
The host key for %s has changed.
Known key: %s in %s:%d
To fix: remove the old key with
  ssh-keygen -R %s`, hostname, hostname, want.Key.Type(), want.Filename, want.Line, hostname)
}

func hasPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
