package cli

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/server"
)

func TestMainCLISubprocessHelper(t *testing.T) {
	if os.Getenv("AMUX_MAIN_HELPER") != "1" {
		return
	}

	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--" {
			os.Args = append([]string{"amux"}, args[i+1:]...)
			os.Exit(Run("", os.Args[1:]))
		}
	}
	t.Fatal("missing -- separator")
}

func runHermeticMain(t *testing.T, args ...string) (output string, exitCode int) {
	t.Helper()

	cmd := newHermeticMainCmd(t, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode()
}

func runHermeticMainWithTimeout(t *testing.T, timeout time.Duration, args ...string) (output string, exitCode int, timedOut bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := newHermeticMainCmdContext(t, ctx, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return string(out), -1, true
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("helper error = %v\n%s", err, out)
	}
	return string(out), exitErr.ExitCode(), false
}

func newHermeticMainCmd(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	return newHermeticMainCmdContext(t, context.Background(), args...)
}

func newHermeticMainCmdContext(t *testing.T, ctx context.Context, args ...string) *exec.Cmd {
	t.Helper()

	session := hermeticMainSession(t.Name())
	cmdArgs := append([]string{"-test.run=TestMainCLISubprocessHelper", "--", "-s", session}, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
	cmd.Env = hermeticMainEnv()
	return cmd
}

func hermeticMainSession(testName string) string {
	var b strings.Builder
	for _, r := range testName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 16 {
			break
		}
	}
	suffix := strings.Trim(b.String(), "-")
	if suffix == "" {
		suffix = "main-usage"
	}
	return fmt.Sprintf("usage-%d-%s-%08x", os.Getpid(), suffix, crc32.ChecksumIEEE([]byte(testName)))
}

func hermeticMainEnv() []string {
	env := append([]string{}, os.Environ()...)
	for _, key := range []string{
		"AMUX_MAIN_HELPER",
		"AMUX_PANE",
		"AMUX_SESSION",
		"TMUX",
		"SSH_CONNECTION",
		"SSH_CLIENT",
		"SSH_TTY",
		"TERM",
	} {
		env = removeEnvKey(env, key)
	}
	env = append(env,
		"AMUX_MAIN_HELPER=1",
		"TERM=xterm-256color",
	)
	return env
}

func removeEnvKey(env []string, key string) []string {
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

func hermeticMainSocketPath(t *testing.T) string {
	t.Helper()

	if err := os.MkdirAll(server.SocketDir(), 0700); err != nil {
		t.Fatalf("MkdirAll socket dir: %v", err)
	}
	sockPath := server.SocketPath(hermeticMainSession(t.Name()))
	_ = os.Remove(sockPath)
	t.Cleanup(func() {
		_ = os.Remove(sockPath)
		_ = os.Remove(filepath.Join(server.SocketDir(), filepath.Base(sockPath)+".log"))
	})
	return sockPath
}

func prepareHermeticMissingSocket(t *testing.T) {
	t.Helper()

	sockPath := hermeticMainSocketPath(t)
	_ = os.Remove(sockPath)
}

func prepareHermeticStaleSocket(t *testing.T) {
	t.Helper()

	sockPath := hermeticMainSocketPath(t)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix(%q): %v", sockPath, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close stale socket listener: %v", err)
	}
}

func prepareHermeticPermissionDeniedSocket(t *testing.T) {
	t.Helper()

	sockPath := hermeticMainSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0); err != nil {
		_ = ln.Close()
		t.Fatalf("Chmod(%q, 0): %v", sockPath, err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(sockPath, 0700)
		_ = ln.Close()
	})
}

func assertMainCommandConnectError(t *testing.T, out, cmdName string) {
	t.Helper()

	want := fmt.Sprintf("amux %s: connecting to server:", cmdName)
	if !strings.Contains(out, want) {
		t.Fatalf("output = %q, want substring %q", out, want)
	}
}

func TestMainEqualizeUsage(t *testing.T) {
	t.Parallel()

	output, exitCode := runHermeticMain(t, "equalize", "--bogus")
	if exitCode == 0 {
		t.Fatalf("exit code = %d, want non-zero\noutput:\n%s", exitCode, output)
	}
	if !strings.Contains(output, `amux equalize: unknown equalize arg "--bogus"`) {
		t.Fatalf("output = %q, want equalize parse error", output)
	}
	if !strings.Contains(output, "usage: amux equalize [--vertical|--all]") {
		t.Fatalf("output = %q, want equalize usage", output)
	}
}

func TestMainDebugHelp(t *testing.T) {
	t.Parallel()

	output, exitCode := runHermeticMain(t, "debug", "--help")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if !strings.Contains(output, "usage: amux debug <goroutines|profile|heap|socket|frames|client-goroutines|client-profile|client-heap>") {
		t.Fatalf("output = %q, want debug usage", output)
	}
}

func TestMainDebugFramesDispatchesDedicatedServerCommand(t *testing.T) {
	t.Parallel()

	sockPath := hermeticMainSocketPath(t)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("Listen(%q): %v", sockPath, err)
	}
	t.Cleanup(func() {
		_ = ln.Close()
	})

	wantOutput := `samples: 2
frame duration: p50 1ms  p95 2ms  p99 2ms
cells diffed: p50 10  p95 20  p99 20
ansi bytes emitted: p50 100  p95 200  p99 200
panes composited: p50 2  p95 3  p99 3
last 100 frame times (oldest -> newest): 1ms, 2ms
`

	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			errCh <- fmt.Errorf("Accept: %w", acceptErr)
			return
		}
		defer conn.Close()

		msg, readErr := server.ReadMsg(conn)
		if readErr != nil {
			errCh <- fmt.Errorf("ReadMsg: %w", readErr)
			return
		}
		if msg.Type != server.MsgTypeCommand {
			errCh <- fmt.Errorf("message type = %v, want %v", msg.Type, server.MsgTypeCommand)
			return
		}
		if msg.CmdName != "debug-frames" {
			errCh <- fmt.Errorf("command = %q, want %q", msg.CmdName, "debug-frames")
			return
		}
		if len(msg.CmdArgs) != 0 {
			errCh <- fmt.Errorf("args = %v, want empty", msg.CmdArgs)
			return
		}
		if writeErr := server.WriteMsg(conn, &server.Message{
			Type:      server.MsgTypeCmdResult,
			CmdOutput: wantOutput,
		}); writeErr != nil {
			errCh <- fmt.Errorf("WriteMsg: %w", writeErr)
			return
		}
		errCh <- nil
	}()

	output, exitCode := runHermeticMain(t, "debug", "frames")
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if output != wantOutput {
		t.Fatalf("output = %q, want %q", output, wantOutput)
	}

	select {
	case serverErr := <-errCh:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for debug frames request")
	}
}
