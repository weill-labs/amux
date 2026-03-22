package test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

// eventJSON is a minimal struct for parsing event stream output.
type eventJSON struct {
	Type       string `json:"type"`
	Timestamp  string `json:"ts"`
	Generation uint64 `json:"generation,omitempty"`
	PaneID     uint32 `json:"pane_id,omitempty"`
	PaneName   string `json:"pane_name,omitempty"`
	Host       string `json:"host,omitempty"`
	ActivePane string `json:"active_pane,omitempty"`
	ClientID   string `json:"client_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	TimedOut   bool   `json:"-"` // set by readEvent on timeout
}

// eventStream connects to the server's events command and returns a scanner
// that reads one JSON event per line, plus a close function.
func eventStream(t *testing.T, session string, args ...string) (*bufio.Scanner, func()) {
	t.Helper()
	sockPath := server.SocketPath(session)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	if err := server.WriteMsg(conn, &server.Message{
		Type:    server.MsgTypeCommand,
		CmdName: "events",
		CmdArgs: args,
	}); err != nil {
		conn.Close()
		t.Fatalf("write: %v", err)
	}

	pr, pw := net.Pipe()
	go func() {
		defer pw.Close()
		for {
			msg, err := server.ReadMsg(conn)
			if err != nil {
				return
			}
			if msg.CmdOutput != "" {
				pw.Write([]byte(msg.CmdOutput))
			}
		}
	}()

	scanner := bufio.NewScanner(pr)
	closer := func() {
		conn.Close()
		pr.Close()
	}
	return scanner, closer
}

type eventsCLIProcess struct {
	t       testing.TB
	cmd     *exec.Cmd
	scanner *bufio.Scanner
	stderr  bytes.Buffer
	done    chan struct{}
	mu      sync.Mutex
	waitErr error
}

func startEventsCLI(t *testing.T, h *ServerHarness, env []string, args ...string) *eventsCLIProcess {
	t.Helper()

	cmdArgs := append([]string{"-s", h.session, "events"}, args...)
	cmd := exec.Command(amuxBin, cmdArgs...)

	envVars := upsertEnv(os.Environ(), "HOME", h.home)
	if h.coverDir != "" {
		envVars = upsertEnv(envVars, "GOCOVERDIR", h.coverDir)
	}
	envVars = append(envVars, h.extraEnv...)
	envVars = append(envVars, env...)
	cmd.Env = envVars

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	proc := &eventsCLIProcess{
		t:       t,
		cmd:     cmd,
		scanner: bufio.NewScanner(stdout),
		done:    make(chan struct{}),
	}
	cmd.Stderr = &proc.stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start events CLI: %v", err)
	}

	go func() {
		err := cmd.Wait()
		proc.mu.Lock()
		proc.waitErr = err
		proc.mu.Unlock()
		close(proc.done)
	}()

	t.Cleanup(func() {
		select {
		case <-proc.done:
			return
		default:
		}
		_ = cmd.Process.Kill()
		<-proc.done
	})

	return proc
}

func (p *eventsCLIProcess) wait(timeout time.Duration) error {
	p.t.Helper()

	select {
	case <-p.done:
		p.mu.Lock()
		err := p.waitErr
		p.mu.Unlock()
		return err
	case <-time.After(timeout):
		p.t.Fatalf("timeout waiting for events CLI exit")
		return nil
	}
}

func (p *eventsCLIProcess) stderrString() string {
	p.t.Helper()
	return p.stderr.String()
}

// readEvent reads the next event from the scanner within timeout.
// Returns a zero eventJSON with TimedOut=true if the deadline expires.
func readEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	done := make(chan eventJSON, 1)
	go func() {
		if scanner.Scan() {
			var ev eventJSON
			if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
				return
			}
			done <- ev
		}
	}()

	select {
	case ev := <-done:
		return ev
	case <-time.After(timeout):
		return eventJSON{TimedOut: true}
	}
}

// mustReadEvent reads the next event, fataling on timeout.
func mustReadEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) eventJSON {
	t.Helper()
	ev := readEvent(t, scanner, timeout)
	if ev.TimedOut {
		t.Fatal("timeout reading event")
	}
	return ev
}

// countEvents counts events of the given type received within a time window.
func countEvents(t *testing.T, scanner *bufio.Scanner, eventType string, window time.Duration) int {
	t.Helper()
	var count int
	deadline := time.After(window)
	for {
		ev := readEvent(t, scanner, window+100*time.Millisecond)
		if ev.TimedOut {
			return count
		}
		if ev.Type == eventType {
			count++
		}
		select {
		case <-deadline:
			return count
		default:
		}
	}
}
