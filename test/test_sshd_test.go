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
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
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
		MaxAuthTries: 20,
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
				handleSSHConn(tcpConn, config)
			}()
		}
	}()

	return ln.Addr().String()
}

// handleSSHConn performs the SSH handshake and dispatches channels.
func handleSSHConn(tcpConn net.Conn, config *ssh.ServerConfig) {
	defer tcpConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		return // auth failure or client disconnect
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
			ch, chReqs, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go handleSession(ch, chReqs)

		case "direct-streamlocal@openssh.com":
			handleStreamLocal(newChannel)

		default:
			newChannel.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

// handleSession handles SSH session channels (exec requests).
func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		switch req.Type {
		case "exec":
			if len(req.Payload) < 4 {
				req.Reply(false, nil)
				continue
			}
			cmdLen := binary.BigEndian.Uint32(req.Payload[:4])
			command := string(req.Payload[4 : 4+cmdLen])
			req.Reply(true, nil)

			cmd := exec.Command("sh", "-c", command)
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
	tmpDir := t.TempDir()
	keyPath := tmpDir + "/id_test"
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
	out, err := exec.Command("id", "-un").Output()
	if err != nil {
		return "root"
	}
	return strings.TrimSpace(string(out))
}
