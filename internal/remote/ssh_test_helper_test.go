package remote

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testSSH holds the state for an in-process SSH test server.
// The server runs on localhost with public key auth, executes commands
// via `sh -c`, and pipes stdin to the command (needed for uploadBinary).
type testSSH struct {
	Client  *ssh.Client
	HomeDir string // fake HOME on the "remote" (actually localhost)
	KeyFile string // path to the private key file
	Addr    string // host:port of the test SSH server
}

// startTestSSH launches an in-process SSH server and returns a connected client.
// The server uses a temp directory as HOME so uploadBinary writes to
// $HOME/.local/bin/amux instead of clobbering the real binary.
// Cleanup is automatic via t.Cleanup.
func startTestSSH(t *testing.T) *testSSH {
	t.Helper()

	// Generate key pair
	pubEd, privEd, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pubEd)
	if err != nil {
		t.Fatalf("converting public key: %v", err)
	}

	// Write private key to temp file
	block, err := ssh.MarshalPrivateKey(privEd, "")
	if err != nil {
		t.Fatalf("marshaling private key: %v", err)
	}
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_test")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatalf("writing key: %v", err)
	}

	// Create fake HOME for the "remote"
	homeDir := t.TempDir()

	// SSH server config
	authorizedKeyBytes := sshPub.Marshal()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPriv)

	srvCfg := &ssh.ServerConfig{
		MaxAuthTries: 20,
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(key.Marshal(), authorizedKeyBytes) {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("unauthorized")
		},
	}
	srvCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})

	// Build exec env with fake HOME and minimal PATH.
	// Strip real amux from PATH so remoteBuildHash finds only what we plant.
	execEnv := os.Environ()
	for i, e := range execEnv {
		if len(e) > 5 && e[:5] == "HOME=" {
			execEnv[i] = "HOME=" + homeDir
		}
		if len(e) > 5 && e[:5] == "PATH=" {
			execEnv[i] = "PATH=" + filepath.Join(homeDir, ".local", "bin") + ":/usr/bin:/bin"
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			tcpConn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				testHandleSSHConn(tcpConn, srvCfg, execEnv)
			}()
		}
	}()

	// Dial the test server
	signer, err := ssh.NewSignerFromKey(privEd)
	if err != nil {
		t.Fatalf("creating signer: %v", err)
	}
	clientCfg := &ssh.ClientConfig{
		User:            "testuser",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	client, err := ssh.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("SSH dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	return &testSSH{
		Client:  client,
		HomeDir: homeDir,
		KeyFile: keyPath,
		Addr:    ln.Addr().String(),
	}
}

func testHandleSSHConn(tcpConn net.Conn, config *ssh.ServerConfig, execEnv []string) {
	defer tcpConn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go testHandleSession(ch, chReqs, execEnv)
	}
}

func testHandleSession(ch ssh.Channel, reqs <-chan *ssh.Request, execEnv []string) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
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

		exitMsg := make([]byte, 4)
		binary.BigEndian.PutUint32(exitMsg, uint32(exitCode))
		ch.SendRequest("exit-status", false, exitMsg)
		return
	}
}
