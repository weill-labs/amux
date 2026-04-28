package client

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/dialutil"
	"github.com/weill-labs/amux/internal/proto"
	"github.com/weill-labs/amux/internal/transport"
	_ "github.com/weill-labs/amux/internal/transport/mosh"
	_ "github.com/weill-labs/amux/internal/transport/ssh"
	"golang.org/x/term"
)

type sshSessionTarget struct {
	Target     transport.Target
	Transport  string
	HostConfig config.Host
	HostRef    string
}

type sshSessionState struct {
	transport transport.Transport
}

type sshRunSessionOps struct {
	newTransport       func(string, config.Host) (transport.Transport, error)
	deployBinary       func(transport.Transport, transport.Target, string) error
	ensureRemoteServer func(transport.Transport, transport.Target, string) error
	dialRemoteSocket   func(transport.Transport, transport.Target) (net.Conn, error)
}

func RunSSHSession(target transport.Target) error {
	resolved, err := resolveSSHSessionTarget(target)
	if err != nil {
		return err
	}
	return runSSHSessionViaLocalServer(resolved, term.GetSize, proto.EnsureDaemon, runLocalServerCommand, runSessionWithDeps)
}

func runSSHSessionViaLocalServer(
	resolved sshSessionTarget,
	getTermSize func(int) (int, int, error),
	ensureDaemon func(string, time.Duration) error,
	runCommand func(string, string, []string) error,
	runner func(string, func(int) (int, int, error), runSessionDeps) error,
) error {
	if err := ensureDaemon(resolved.Target.Session, 5*time.Second); err != nil {
		return fmt.Errorf("ensuring local server: %w", err)
	}
	if err := runCommand(resolved.Target.Session, "connect", []string{resolved.HostRef}); err != nil {
		return fmt.Errorf("connecting remote host: %w", err)
	}
	return runner(resolved.Target.Session, getTermSize, defaultRunSessionDeps())
}

func runSSHSession(
	target transport.Target,
	getTermSize func(int) (int, int, error),
	ops sshRunSessionOps,
	runner func(string, func(int) (int, int, error), runSessionDeps) error,
) error {
	resolved, err := resolveSSHSessionTarget(target)
	if err != nil {
		return err
	}

	state := &sshSessionState{}
	defer state.close()

	return runner(resolved.Target.Session, getTermSize, sshRunSessionDeps(resolved, state, ops))
}

func resolveSSHSessionTarget(target transport.Target) (sshSessionTarget, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return sshSessionTarget{}, fmt.Errorf("loading config: %w", err)
	}

	resolved := sshSessionTarget{
		Target:    target,
		Transport: cfg.HostTransport(target.Host),
		HostRef:   target.Host,
	}
	if hostCfg, ok := cfg.Hosts[target.Host]; ok {
		resolved.HostConfig = hostCfg
	}
	resolved.HostConfig.TransportPreference = cfg.TransportPreferences()
	configuredUser := cfg.HostUser(target.Host)
	if resolved.HostConfig.User == "" {
		resolved.HostConfig.User = configuredUser
	}
	if target.User != "" && target.User != configuredUser {
		resolved.HostRef = target.User + "@" + target.Host
	}
	return resolved, nil
}

func defaultSSHRunSessionOps() sshRunSessionOps {
	return sshRunSessionOps{
		newTransport: func(name string, cfg config.Host) (transport.Transport, error) {
			return transport.Get(name, cfg)
		},
		deployBinary: func(tr transport.Transport, target transport.Target, buildHash string) error {
			return tr.Deploy(context.Background(), target, buildHash)
		},
		ensureRemoteServer: func(tr transport.Transport, target transport.Target, sessionName string) error {
			return tr.EnsureServer(context.Background(), target, sessionName)
		},
		dialRemoteSocket: func(tr transport.Transport, target transport.Target) (net.Conn, error) {
			return tr.Dial(context.Background(), target)
		},
	}
}

func sshRunSessionDeps(target sshSessionTarget, state *sshSessionState, ops sshRunSessionOps) runSessionDeps {
	deps := defaultRunSessionDeps()
	deps.ensureDaemon = func(sessionName string, timeout time.Duration) error {
		tr, err := connectSSHSession(target, sessionName, timeout, ops)
		if err != nil {
			return err
		}
		state.set(tr)
		return nil
	}
	deps.dial = func(string, string) (net.Conn, error) {
		if state.transport == nil {
			return nil, fmt.Errorf("ssh transport not initialized")
		}
		return ops.dialRemoteSocket(state.transport, target.Target)
	}
	return deps
}

func connectSSHSession(target sshSessionTarget, sessionName string, timeout time.Duration, ops sshRunSessionOps) (transport.Transport, error) {
	sessionTarget := target.Target
	sessionTarget.Session = sessionName

	tr, err := ops.newTransport(target.Transport, target.HostConfig)
	if err != nil {
		return nil, fmt.Errorf("creating %s transport: %w", target.Transport, err)
	}

	// Keep deploy best-effort so connection still works when upload fails.
	_ = ops.deployBinary(tr, sessionTarget, clientBuildHash())

	if err := ops.ensureRemoteServer(tr, sessionTarget, sessionName); err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("starting remote server: %w", err)
	}
	if err := waitForSSHRemoteSocket(tr, sessionTarget, timeout, ops.dialRemoteSocket); err != nil {
		_ = tr.Close()
		return nil, err
	}
	return tr, nil
}

func waitForSSHRemoteSocket(
	tr transport.Transport,
	target transport.Target,
	timeout time.Duration,
	dialRemoteSocket func(transport.Transport, transport.Target) (net.Conn, error),
) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := dialRemoteSocket(tr, target)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket for session %s did not appear within %v", target.Session, timeout)
}

func (s *sshSessionState) close() {
	if s.transport == nil {
		return
	}
	_ = s.transport.Close()
	s.transport = nil
}

func (s *sshSessionState) set(tr transport.Transport) {
	s.close()
	s.transport = tr
}

func runLocalServerCommand(sessionName, cmdName string, args []string) error {
	conn, err := dialutil.DialUnix(proto.SocketPath(sessionName))
	if err != nil {
		return err
	}
	defer conn.Close()

	writer := proto.NewWriter(conn)
	reader := proto.NewReader(conn)
	if err := writer.WriteMsg(&proto.Message{
		Type:    proto.MsgTypeCommand,
		CmdName: cmdName,
		CmdArgs: args,
	}); err != nil {
		return err
	}

	for {
		reply, err := reader.ReadMsg()
		if err != nil {
			return err
		}
		if reply.Type != proto.MsgTypeCmdResult {
			continue
		}
		if reply.CmdErr != "" {
			return fmt.Errorf("%s", reply.CmdErr)
		}
		return nil
	}
}
