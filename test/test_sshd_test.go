package test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

type testSSHServerOptions struct {
	preloadAmux bool
}

type testSSHServer struct {
	Addr    string
	HomeDir string
}

type testSSHFixture struct {
	Addr    string
	KeyFile string
	HomeDir string
}

// startTestSSHServer starts a lightweight in-process SSH server that:
//   - Listens on localhost:0 (random port)
//   - Accepts public key auth with the given authorized key
//   - Handles "session"/"exec" requests by running commands via sh -c
//   - Handles "direct-streamlocal@openssh.com" by dialing Unix sockets
//
// Returns the server address (host:port) and remote HOME directory.
// The server is shut down via t.Cleanup.
func startTestSSHServer(t *testing.T, authorizedKey ssh.PublicKey, opts testSSHServerOptions) testSSHServer {
	t.Helper()

	homeDir := t.TempDir()
	execEnv := buildTestSSHExecEnv(homeDir, opts)

	// Generate ed25519 host key
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatalf("creating host signer: %v", err)
	}

	authorizedKeyBytes := authorizedKey.Marshal()
	config := &ssh.ServerConfig{
		MaxAuthTries: 20, // Allow many attempts — SSH agent may present many keys
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKeyBytes) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized key")
		},
	}
	config.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}

	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
		killAmuxServersForHome(homeDir)
	})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			tcpConn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleSSHConn(tcpConn, config, execEnv)
			}()
		}
	}()

	return testSSHServer{
		Addr:    ln.Addr().String(),
		HomeDir: homeDir,
	}
}

func buildTestSSHExecEnv(homeDir string, opts testSSHServerOptions) []string {
	execEnv := append([]string{}, os.Environ()...)
	for _, key := range []string{
		"AMUX_PANE",
		"AMUX_SESSION",
		"TMUX",
		"SSH_CONNECTION",
		"SSH_CLIENT",
		"SSH_TTY",
	} {
		execEnv = removeEnv(execEnv, key)
	}
	execEnv = upsertEnv(execEnv, "HOME", homeDir)

	if !opts.preloadAmux {
		// Keep only base system tools on PATH so the remote starts without any
		// usable amux binary unless HostConn auto-deploy uploads one.
		execEnv = upsertEnv(execEnv, "PATH", "/usr/bin:/bin")
		return removeEnv(execEnv, "AMUX_BIN")
	}

	execEnv = upsertEnv(execEnv, "PATH", filepath.Dir(amuxBin)+":"+os.Getenv("PATH"))
	// Force the happy-path fixture to use the freshly built test binary rather
	// than any previously installed amux on the machine.
	return upsertEnv(execEnv, "AMUX_BIN", amuxBin)
}

func removeEnv(env []string, key string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func amuxServerPIDsForHome(homeDir string) []string {
	out, err := exec.Command("pgrep", "-f", "amux _server ").Output()
	if err != nil {
		return nil
	}

	var pids []string
	for _, pid := range strings.Fields(string(out)) {
		psOut, err := exec.Command("ps", "eww", "-p", pid, "-o", "command=").Output()
		if err != nil {
			continue
		}
		if strings.Contains(string(psOut), "HOME="+homeDir) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func killAmuxServersForHome(homeDir string) {
	for _, pid := range amuxServerPIDsForHome(homeDir) {
		_ = exec.Command("kill", pid).Run()
	}

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if len(amuxServerPIDsForHome(homeDir)) == 0 {
			return
		}
		select {
		case <-deadline:
			return
		case <-ticker.C:
		}
	}
}

// handleSSHConn performs the SSH handshake and dispatches channels.
func handleSSHConn(tcpConn net.Conn, config *ssh.ServerConfig, execEnv []string) {
	defer tcpConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		return // auth failure or client disconnect
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	// Build SSH_CONNECTION from the real TCP addresses so that commands run
	// inside this session (e.g. "amux") can detect they're in an SSH context
	// and trigger the takeover flow. Format: "clientIP clientPort serverIP serverPort"
	remoteAddr := tcpConn.RemoteAddr().String()
	localAddr := tcpConn.LocalAddr().String()
	remoteHost, remotePort, _ := net.SplitHostPort(remoteAddr)
	localHost, localPort, _ := net.SplitHostPort(localAddr)
	sshConnectionVal := fmt.Sprintf("%s %s %s %s", remoteHost, remotePort, localHost, localPort)
	connEnv := upsertEnv(append([]string{}, execEnv...), "SSH_CONNECTION", sshConnectionVal)

	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
			ch, chReqs, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go handleSession(ch, chReqs, connEnv)

		case "direct-streamlocal@openssh.com":
			handleStreamLocal(newChannel)

		default:
			newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

type ptyRequest struct {
	Term     string
	Cols     uint32
	Rows     uint32
	WidthPx  uint32
	HeightPx uint32
	Modes    string
}

type windowChangeRequest struct {
	Cols     uint32
	Rows     uint32
	WidthPx  uint32
	HeightPx uint32
}

// handleSession handles SSH session channels (exec and interactive shell requests).
func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, execEnv []string) {
	defer ch.Close()
	winSize := &pty.Winsize{Cols: 80, Rows: 24}

	termType := "xterm-256color"

	for req := range reqs {
		switch req.Type {
		case "env":
			req.Reply(true, nil)

		case "pty-req":
			var ptyReq ptyRequest
			if err := ssh.Unmarshal(req.Payload, &ptyReq); err != nil {
				req.Reply(false, nil)
				continue
			}
			winSize = &pty.Winsize{
				Cols: uint16(max(1, ptyReq.Cols)),
				Rows: uint16(max(1, ptyReq.Rows)),
			}
			if ptyReq.Term != "" {
				termType = ptyReq.Term
			}
			req.Reply(true, nil)

		case "shell":
			req.Reply(true, nil)
			runShellSession(ch, reqs, execEnv, winSize, termType)
			return

		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
			command := string(req.Payload[4 : 4+cmdLen])
			req.Reply(true, nil)

			cmd := exec.Command("sh", "-c", command)
			cmd.Env = upsertEnv(append([]string{}, execEnv...), "TERM", termType)
			cmd.Stdout = ch
			cmd.Stderr = ch.Stderr()
			cmd.Stdin = ch

			exitCode := 0
			if err := cmd.Run(); err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					exitCode = 1
				}
			}

			// Send exit-status
			exitMsg := make([]byte, 4)
			binary.BigEndian.PutUint32(exitMsg, uint32(exitCode))
			ch.SendRequest("exit-status", false, exitMsg)
			return

		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func runShellSession(ch ssh.Channel, reqs <-chan *ssh.Request, execEnv []string, size *pty.Winsize, termType string) {
	var sizeMu sync.RWMutex
	currentSize := *size

	go func() {
		for req := range reqs {
			switch req.Type {
			case "window-change":
				var wc windowChangeRequest
				if err := ssh.Unmarshal(req.Payload, &wc); err == nil {
					sizeMu.Lock()
					currentSize = pty.Winsize{
						Cols: uint16(max(1, wc.Cols)),
						Rows: uint16(max(1, wc.Rows)),
					}
					sizeMu.Unlock()
				}
				if req.WantReply {
					req.Reply(true, nil)
				}
			case "signal":
				if req.WantReply {
					req.Reply(true, nil)
				}
			default:
				if req.WantReply {
					req.Reply(false, nil)
				}
			}
		}
	}()

	term := term.NewTerminal(ch, "AMUX_TEST_SSH_PROMPT$ ")
	for {
		line, err := term.ReadLine()
		if err != nil {
			sendExitStatus(ch, 0)
			return
		}

		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == "exit":
			sendExitStatus(ch, 0)
			return
		case strings.HasPrefix(line, "echo "):
			_, _ = io.WriteString(ch, strings.TrimPrefix(line, "echo ")+"\r\n")
		default:
			sizeMu.RLock()
			ws := currentSize
			sizeMu.RUnlock()
			sendExitStatus(ch, runShellCommand(ch, line, execEnv, &ws, termType))
			return
		}
	}
}

func sendExitStatus(ch ssh.Channel, exitCode int) {
	exitMsg := make([]byte, 4)
	binary.BigEndian.PutUint32(exitMsg, uint32(exitCode))
	ch.SendRequest("exit-status", false, exitMsg)
}

type crToLFWriter struct {
	w io.Writer
}

func runShellCommand(ch ssh.Channel, command string, execEnv []string, size *pty.Winsize, termType string) int {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(append([]string{}, execEnv...), "TERM="+termType)

	ptmx, err := pty.StartWithSize(cmd, size)
	if err != nil {
		_, _ = io.WriteString(ch, err.Error()+"\r\n")
		return 1
	}
	defer ptmx.Close()

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(ch, ptmx)
		close(done)
	}()
	go func() {
		_, _ = io.Copy(ptmx, ch)
	}()

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	_ = ptmx.Close()
	<-done
	return exitCode
}

// streamLocalData is the wire format for direct-streamlocal@openssh.com.
type streamLocalData struct {
	SocketPath string
	Reserved0  string
	Reserved1  uint32
}

// handleStreamLocal handles direct-streamlocal@openssh.com channels
// by dialing the requested Unix socket and proxying bytes bidirectionally.
func handleStreamLocal(newChannel ssh.NewChannel) {
	var data streamLocalData
	if err := ssh.Unmarshal(newChannel.ExtraData(), &data); err != nil {
		newChannel.Reject(ssh.ConnectionFailed, "invalid channel data")
		return
	}

	unixConn, err := net.Dial("unix", data.SocketPath)
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, fmt.Sprintf("dial unix %s: %v", data.SocketPath, err))
		return
	}

	ch, _, err := newChannel.Accept()
	if err != nil {
		unixConn.Close()
		return
	}

	// Bidirectional proxy
	go func() {
		io.Copy(ch, unixConn)
		ch.CloseWrite()
	}()
	go func() {
		io.Copy(unixConn, ch)
		unixConn.Close()
	}()
}

// generateTestKeyPair creates an ed25519 key pair and returns the SSH public
// key (for server auth config) and PEM-encoded private key in OpenSSH format
// (for writing to a temp file that the amux config can reference).
func generateTestKeyPair(t *testing.T) (ssh.PublicKey, []byte) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating ed25519 key: %v", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("converting public key: %v", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshaling private key: %v", err)
	}

	return sshPub, pem.EncodeToMemory(block)
}

// setupTestSSH starts an in-process SSH server and returns the address
// and path to a temp private key file. No system sshd or authorized_keys
// manipulation required — works identically on macOS and Linux.
func setupTestSSH(t *testing.T) (addr string, keyFile string) {
	t.Helper()
	fixture := setupTestSSHWithOptions(t, testSSHServerOptions{preloadAmux: true})
	return fixture.Addr, fixture.KeyFile
}

// setupTestSSHNoPreload starts the SSH fixture without AMUX_BIN and without
// a preloaded amux in PATH. Remote connect must deploy ~/.local/bin/amux to
// make the session functional.
func setupTestSSHNoPreload(t *testing.T) testSSHFixture {
	t.Helper()
	return setupTestSSHWithOptions(t, testSSHServerOptions{})
}

func setupTestSSHWithOptions(t *testing.T, opts testSSHServerOptions) testSSHFixture {
	t.Helper()
	pubKey, privPEM := generateTestKeyPair(t)
	server := startTestSSHServer(t, pubKey, opts)

	// Write private key to a temp file for the amux identity_file config
	keyPath := filepath.Join(t.TempDir(), "id_test")
	if err := os.WriteFile(keyPath, privPEM, 0600); err != nil {
		t.Fatalf("writing test key: %v", err)
	}

	return testSSHFixture{
		Addr:    server.Addr,
		KeyFile: keyPath,
		HomeDir: server.HomeDir,
	}
}

// remoteTestConfig returns a TOML config with a "test-remote" host pointing
// at the given address (host:port) using the given identity file.
func remoteTestConfig(addr, identityFile string) string {
	host, port, _ := net.SplitHostPort(addr)
	user := currentUser()
	return fmt.Sprintf(`
[hosts.test-remote]
type = "remote"
user = "%s"
address = "%s:%s"
identity_file = "%s"
`, user, host, port, identityFile)
}

func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return "root"
	}
	return u.Username
}
