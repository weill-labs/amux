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

// startTestSSHServer starts a lightweight in-process SSH server that:
//   - Listens on localhost:0 (random port)
//   - Accepts public key auth with the given authorized key
//   - Handles "session"/"exec" requests by running commands via sh -c
//   - Handles "direct-streamlocal@openssh.com" by dialing Unix sockets
//
// Returns the server address (host:port).
// The server is shut down via t.Cleanup.
func startTestSSHServer(t *testing.T, authorizedKey ssh.PublicKey) string {
	t.Helper()

	// Build env for exec'd commands with the test amux binary in PATH.
	// On CI runners the binary is in a temp dir not in PATH.
	execEnv := os.Environ()
	binDir := filepath.Dir(amuxBin)
	homeDir := t.TempDir()
	sawPath := false
	sawHome := false
	if !strings.Contains(os.Getenv("PATH"), binDir) {
		for i, e := range execEnv {
			if strings.HasPrefix(e, "PATH=") {
				sawPath = true
				execEnv[i] = "PATH=" + binDir + ":" + e[5:]
				break
			}
		}
	}
	for i, e := range execEnv {
		if strings.HasPrefix(e, "HOME=") {
			sawHome = true
			execEnv[i] = "HOME=" + homeDir
		}
	}
	if !sawPath {
		execEnv = append(execEnv, "PATH="+binDir+":"+os.Getenv("PATH"))
	}
	if !sawHome {
		execEnv = append(execEnv, "HOME="+homeDir)
	}
	// Override the binary used by buildEnsureServerCmd so the remote server
	// always uses the test binary, not a stale ~/.local/bin/amux.
	execEnv = append(execEnv, "AMUX_BIN="+amuxBin)

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

	return ln.Addr().String()
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
	connEnv := append(append([]string{}, execEnv...), "SSH_CONNECTION="+sshConnectionVal)

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
			req.Reply(true, nil)

		case "shell":
			req.Reply(true, nil)
			runShellSession(ch, reqs, execEnv, winSize)
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
			cmd.Env = execEnv
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

func runShellSession(ch ssh.Channel, reqs <-chan *ssh.Request, execEnv []string, size *pty.Winsize) {
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
			sendExitStatus(ch, runShellCommand(ch, line, execEnv, &ws))
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

func runShellCommand(ch ssh.Channel, command string, execEnv []string, size *pty.Winsize) int {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(append([]string{}, execEnv...), "TERM=xterm-256color")

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

	pubKey, privPEM := generateTestKeyPair(t)
	addr = startTestSSHServer(t, pubKey)

	// Write private key to a temp file for the amux identity_file config
	keyPath := filepath.Join(t.TempDir(), "id_test")
	if err := os.WriteFile(keyPath, privPEM, 0600); err != nil {
		t.Fatalf("writing test key: %v", err)
	}

	return addr, keyPath
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
