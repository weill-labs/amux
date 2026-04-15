package client

import (
	"fmt"
	"net"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/remote"
	"github.com/weill-labs/amux/internal/sshutil"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type sshSessionTarget struct {
	sshutil.SSHTarget
	Address      string
	IdentityFile string
}

type sshSessionState struct {
	client   *ssh.Client
	sockPath string
}

type sshRunSessionOps struct {
	buildSSHConfig     func(string, string) (*ssh.ClientConfig, error)
	sshDial            func(string, string, *ssh.ClientConfig) (*ssh.Client, error)
	sshOutput          func(*ssh.Client, string) (string, error)
	deployBinary       func(*ssh.Client, string) error
	ensureRemoteServer func(*ssh.Client, string, string) error
	dialRemoteSocket   func(*ssh.Client, string) (net.Conn, error)
}

func RunSSHSession(target sshutil.SSHTarget) error {
	resolved, err := resolveSSHSessionTarget(target)
	if err != nil {
		return err
	}

	state := &sshSessionState{}
	defer state.close()

	return runSessionWithDeps(resolved.Session, term.GetSize, sshRunSessionDeps(resolved, state, defaultSSHRunSessionOps()))
}

func resolveSSHSessionTarget(target sshutil.SSHTarget) (sshSessionTarget, error) {
	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return sshSessionTarget{}, fmt.Errorf("loading config: %w", err)
	}

	resolved := sshSessionTarget{
		SSHTarget: target,
		Address:   target.Host,
	}
	if hostCfg, ok := cfg.Hosts[target.Host]; ok {
		resolved.IdentityFile = hostCfg.IdentityFile
		if hostCfg.Address != "" {
			resolved.Address = hostCfg.Address
		}
	}
	resolved.Address = resolveSSHAddress(resolved.Address, target.Port)
	return resolved, nil
}

func defaultSSHRunSessionOps() sshRunSessionOps {
	return sshRunSessionOps{
		buildSSHConfig:     sshutil.BuildSSHConfig,
		sshDial:            ssh.Dial,
		sshOutput:          sshutil.SSHOutput,
		deployBinary:       remote.DeployBinary,
		ensureRemoteServer: sshutil.EnsureRemoteServer,
		dialRemoteSocket:   sshutil.DialRemoteSocket,
	}
}

func sshRunSessionDeps(target sshSessionTarget, state *sshSessionState, ops sshRunSessionOps) runSessionDeps {
	deps := defaultRunSessionDeps()
	deps.ensureDaemon = func(sessionName string, timeout time.Duration) error {
		sshCfg, err := ops.buildSSHConfig(target.User, target.IdentityFile)
		if err != nil {
			return fmt.Errorf("building SSH config: %w", err)
		}

		sshClient, err := ops.sshDial("tcp", target.Address, sshCfg)
		if err != nil {
			return fmt.Errorf("SSH dial: %w", err)
		}

		remoteUID, err := ops.sshOutput(sshClient, "id -u")
		if err != nil {
			_ = sshClient.Close()
			return fmt.Errorf("querying remote UID: %w", err)
		}

		// Keep deploy best-effort so connection still works when upload fails.
		_ = ops.deployBinary(sshClient, clientBuildHash())

		sockPath := sshutil.RemoteSocketPath(remoteUID, sessionName)
		if err := ops.ensureRemoteServer(sshClient, sockPath, sessionName); err != nil {
			_ = sshClient.Close()
			return fmt.Errorf("starting remote server: %w", err)
		}
		if err := waitForSSHRemoteSocket(sshClient, sockPath, timeout, ops.sshOutput); err != nil {
			_ = sshClient.Close()
			return err
		}

		state.set(sshClient, sockPath)
		return nil
	}
	deps.dial = func(string, string) (net.Conn, error) {
		if state.client == nil || state.sockPath == "" {
			return nil, fmt.Errorf("ssh client not initialized")
		}
		return ops.dialRemoteSocket(state.client, state.sockPath)
	}
	return deps
}

func waitForSSHRemoteSocket(client *ssh.Client, sockPath string, timeout time.Duration, sshOutput func(*ssh.Client, string) (string, error)) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := sshOutput(client, fmt.Sprintf("test -S %s && echo ok", sockPath))
		if err == nil && out == "ok" {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("socket %s did not appear within %v", sockPath, timeout)
}

func resolveSSHAddress(addr, port string) string {
	if hasExplicitPort(addr) {
		return addr
	}
	if port != "" && port != "22" {
		return addr + ":" + port
	}
	return sshutil.NormalizeAddr(addr)
}

func hasExplicitPort(addr string) bool {
	_, _, err := net.SplitHostPort(addr)
	return err == nil
}

func (s *sshSessionState) close() {
	if s.client == nil {
		return
	}
	_ = s.client.Close()
	s.client = nil
	s.sockPath = ""
}

func (s *sshSessionState) set(client *ssh.Client, sockPath string) {
	s.close()
	s.client = client
	s.sockPath = sockPath
}
