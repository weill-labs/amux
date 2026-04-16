package ssh

import (
	"context"
	"fmt"
	"net"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/transport"
	gossh "golang.org/x/crypto/ssh"
)

func init() {
	transport.Register("ssh", newSSH)
}

type sshTransport struct {
	cfg      config.Host
	deps     sshTransportDeps
	client   *gossh.Client
	remoteUID string
}

type sshTransportDeps struct {
	buildSSHConfig     func(string, string) (*gossh.ClientConfig, error)
	sshDial            func(string, string, *gossh.ClientConfig) (*gossh.Client, error)
	sshOutput          func(*gossh.Client, string) (string, error)
	deployBinary       func(*gossh.Client, string) error
	ensureRemoteServer func(*gossh.Client, string, string) error
	dialRemoteSocket   func(*gossh.Client, string) (net.Conn, error)
	closeClient        func(*gossh.Client) error
	remoteSocketPath   func(string, string) string
	normalizeAddr      func(string) string
}

func newSSH(cfg config.Host) (transport.Transport, error) {
	return newSSHTransportWithDeps(cfg, sshTransportDeps{}), nil
}

func newSSHTransportWithDeps(cfg config.Host, deps sshTransportDeps) *sshTransport {
	if deps.buildSSHConfig == nil {
		deps.buildSSHConfig = BuildSSHConfig
	}
	if deps.sshDial == nil {
		deps.sshDial = gossh.Dial
	}
	if deps.sshOutput == nil {
		deps.sshOutput = SSHOutput
	}
	if deps.deployBinary == nil {
		deps.deployBinary = DeployBinary
	}
	if deps.ensureRemoteServer == nil {
		deps.ensureRemoteServer = EnsureRemoteServer
	}
	if deps.dialRemoteSocket == nil {
		deps.dialRemoteSocket = DialRemoteSocket
	}
	if deps.closeClient == nil {
		deps.closeClient = func(client *gossh.Client) error {
			return client.Close()
		}
	}
	if deps.remoteSocketPath == nil {
		deps.remoteSocketPath = RemoteSocketPath
	}
	if deps.normalizeAddr == nil {
		deps.normalizeAddr = NormalizeAddr
	}
	return &sshTransport{cfg: cfg, deps: deps}
}

func (t *sshTransport) Name() string {
	return "ssh"
}

func (t *sshTransport) Dial(ctx context.Context, target transport.Target) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := t.ensureClient(target)
	if err != nil {
		return nil, err
	}
	sockPath, err := t.socketPath(client, target.Session)
	if err != nil {
		return nil, err
	}
	return t.deps.dialRemoteSocket(client, sockPath)
}

func (t *sshTransport) Deploy(ctx context.Context, target transport.Target, buildHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	client, err := t.ensureClient(target)
	if err != nil {
		return err
	}
	return t.deps.deployBinary(client, buildHash)
}

func (t *sshTransport) EnsureServer(ctx context.Context, target transport.Target, session string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if session == "" {
		session = target.Session
	}
	client, err := t.ensureClient(target)
	if err != nil {
		return err
	}
	sockPath, err := t.socketPath(client, session)
	if err != nil {
		return err
	}
	return t.deps.ensureRemoteServer(client, sockPath, session)
}

func (t *sshTransport) Close() error {
	if t.client == nil {
		return nil
	}
	client := t.client
	t.client = nil
	t.remoteUID = ""
	return t.deps.closeClient(client)
}

func (t *sshTransport) ensureClient(target transport.Target) (*gossh.Client, error) {
	if t.client != nil {
		return t.client, nil
	}

	sshCfg, err := t.deps.buildSSHConfig(target.User, t.cfg.IdentityFile)
	if err != nil {
		return nil, fmt.Errorf("building SSH config: %w", err)
	}

	client, err := t.deps.sshDial("tcp", resolveTargetAddr(t.cfg, target, t.deps.normalizeAddr), sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial: %w", err)
	}
	t.client = client
	return client, nil
}

func (t *sshTransport) socketPath(client *gossh.Client, session string) (string, error) {
	if session == "" {
		session = "main"
	}
	remoteUID, err := t.remoteUIDFor(client)
	if err != nil {
		return "", fmt.Errorf("querying remote UID: %w", err)
	}
	return t.deps.remoteSocketPath(remoteUID, session), nil
}

func (t *sshTransport) remoteUIDFor(client *gossh.Client) (string, error) {
	if t.remoteUID != "" {
		return t.remoteUID, nil
	}
	remoteUID, err := t.deps.sshOutput(client, "id -u")
	if err != nil {
		return "", err
	}
	t.remoteUID = remoteUID
	return remoteUID, nil
}

func resolveTargetAddr(cfg config.Host, target transport.Target, normalizeAddr func(string) string) string {
	addr := target.Host
	if cfg.Address != "" {
		addr = cfg.Address
	}
	if addr == "" {
		addr = target.Host
	}
	if hasExplicitPort(addr) {
		return addr
	}
	if target.Port != "" && target.Port != "22" {
		return addr + ":" + target.Port
	}
	return normalizeAddr(addr)
}

func hasExplicitPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}
