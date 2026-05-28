package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

func TestDefaultRetryPolicyDelays(t *testing.T) {
	t.Parallel()

	policy := DefaultRetryPolicy()
	if policy.MaxAttempts != 5 {
		t.Fatalf("MaxAttempts = %d, want 5", policy.MaxAttempts)
	}

	var got []time.Duration
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		got = append(got, policy.Delay(attempt))
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
	if got := policy.Delay(10); got != 30*time.Second {
		t.Fatalf("Delay(10) = %v, want cap 30s", got)
	}
	custom := RetryPolicy{InitialBackoff: 0, MaxBackoff: 3 * time.Second}
	if got := custom.Delay(0); got != time.Second {
		t.Fatalf("custom Delay(0) = %v, want default initial 1s", got)
	}
	if got := (RetryPolicy{}).Delay(3); got != 4*time.Second {
		t.Fatalf("zero-value Delay(3) = %v, want defaulted 4s", got)
	}
	if got := (RetryPolicy{InitialBackoff: 2 * time.Second, MaxBackoff: 3 * time.Second}).Delay(2); got != 3*time.Second {
		t.Fatalf("capped Delay(2) = %v, want 3s", got)
	}
}

func TestLinkConnectsWithInjectedDialer(t *testing.T) {
	t.Parallel()

	socketPath := startRemoteEchoSocket(t)
	host := config.Host{
		SSH:        "ignored@example.test",
		Session:    "main",
		SocketPath: socketPath,
	}
	link := NewLink(host, unixSocketDialer{})
	if err := link.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = link.Close() })

	if err := link.WriteMsg(&proto.Message{Type: proto.MsgTypeListPanes, Session: "main"}); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}
	msg, err := link.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if msg.Type != proto.MsgTypeNotify || msg.Text != "echo:main" {
		t.Fatalf("message = %+v, want notify echo", msg)
	}
}

func TestLinkCloseClosesInjectedConnection(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer server.Close()

	link := NewLink(config.Host{SSH: "test", Session: "main", SocketPath: "/tmp/amux-test"}, staticDialer{conn: client})
	if err := link.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := link.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := server.Write([]byte("x")); err == nil {
		t.Fatal("server Write succeeded after Link.Close, want closed pipe")
	}
}

func TestLinkConnectSurfacesDialerError(t *testing.T) {
	t.Parallel()

	dialErr := errors.New("dial refused")
	link := NewLink(config.Host{SSH: "test", Session: "main", SocketPath: "/tmp/amux-test"}, errorDialer{err: dialErr})
	if err := link.Connect(context.Background()); !errors.Is(err, dialErr) {
		t.Fatalf("Connect() error = %v, want %v", err, dialErr)
	}
}

func TestLinkRejectsSecondConnectWithoutRedialing(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer server.Close()

	dialer := &countingDialer{conn: client}
	link := NewLink(config.Host{SSH: "test", Session: "main", SocketPath: "/tmp/amux-test"}, dialer)
	if err := link.Connect(context.Background()); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	t.Cleanup(func() { _ = link.Close() })

	if err := link.Connect(context.Background()); err == nil || err.Error() != "remote link already connected" {
		t.Fatalf("second Connect() error = %v, want already connected", err)
	}
	if dialer.calls != 1 {
		t.Fatalf("dial calls = %d, want 1", dialer.calls)
	}
}

func TestLinkClosedState(t *testing.T) {
	t.Parallel()

	var nilLink *Link
	if err := nilLink.Close(); err != nil {
		t.Fatalf("nil Close() = %v, want nil", err)
	}
	if err := nilLink.Connect(context.Background()); err == nil {
		t.Fatal("nil Connect() error = nil, want error")
	}

	link := NewLink(config.Host{SSH: "test", Session: "main", SocketPath: "/tmp/amux-test"}, nil)
	if err := link.WriteMsg(&proto.Message{Type: proto.MsgTypeNotify}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteMsg before Connect = %v, want net.ErrClosed", err)
	}
	if _, err := link.ReadMsg(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("ReadMsg before Connect = %v, want net.ErrClosed", err)
	}
	if err := link.Close(); err != nil {
		t.Fatalf("Close before Connect = %v, want nil", err)
	}
}

func TestSSHDialerRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host config.Host
		want string
	}{
		{name: "ssh", host: config.Host{SocketPath: "/tmp/amux-test"}, want: "ssh target is required"},
		{name: "ssh starts with dash", host: config.Host{SSH: "-o ProxyCommand=malicious", SocketPath: "/tmp/amux-test"}, want: "ssh target must not start with '-'"},
		{name: "socket", host: config.Host{SSH: "cweill@example.test"}, want: "socket path is required"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := (SSHDialer{}).Dial(context.Background(), tt.host)
			if err == nil || err.Error() != tt.want {
				t.Fatalf("Dial() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSSHDialerSurfacesStartError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	_, err := (SSHDialer{}).Dial(context.Background(), config.Host{
		SSH:        "cweill@example.test",
		Session:    "main",
		SocketPath: "/tmp/amux-test",
	})
	if err == nil || !strings.Contains(err.Error(), "starting ssh:") {
		t.Fatalf("Dial() error = %v, want starting ssh error", err)
	}
}

func TestSSHDialerUsesConfiguredCommandAndStreamsProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh shell script requires Unix")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "ssh.log")
	fakeSSH := filepath.Join(dir, "ssh")
	script := fmt.Sprintf(`#!/bin/sh
printf 'args:%%s\n' "$*" >> %s
trap 'printf exit >> %s' EXIT
cat
`, shellQuote(logPath), shellQuote(logPath))
	if err := os.WriteFile(fakeSSH, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(fake ssh): %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	host := config.Host{
		SSH:        "cweill@example.test",
		Session:    "main",
		SocketPath: "/tmp/amux-1000/main",
	}
	link := NewLink(host, SSHDialer{})
	if err := link.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := link.WriteMsg(&proto.Message{Type: proto.MsgTypeNotify, Text: "hello"}); err != nil {
		t.Fatalf("WriteMsg: %v", err)
	}
	msg, err := link.ReadMsg()
	if err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}
	if msg.Type != proto.MsgTypeNotify || msg.Text != "hello" {
		t.Fatalf("round trip = %+v, want echoed notify", msg)
	}

	if err := link.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	logData := readEventually(t, logPath, "exit")
	wantArgs := "args:-o BatchMode=yes cweill@example.test -- nc -U /tmp/amux-1000/main"
	if !strings.Contains(logData, wantArgs) {
		t.Fatalf("fake ssh log = %q, want args %q", logData, wantArgs)
	}
}

func TestCommandConnCloseCancelsOnNormalExit(t *testing.T) {
	t.Parallel()

	cancelled := false
	conn := &commandConn{
		cancel: func() { cancelled = true },
		stdin:  nopWriteCloser{},
		stdout: nopReadCloser{},
		waitCh: closedWait(nil),
	}

	if err := conn.closeWithTimeout(time.Second, time.Second); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if !cancelled {
		t.Fatal("cancel was not called on normal close")
	}
}

func TestCommandConnCloseBoundsWaitAfterKill(t *testing.T) {
	t.Parallel()

	cancelled := false
	conn := &commandConn{
		cancel: func() { cancelled = true },
		stdin:  nopWriteCloser{},
		stdout: nopReadCloser{},
		waitCh: make(chan error),
	}

	start := time.Now()
	err := conn.closeWithTimeout(time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "waiting for ssh process after kill") {
		t.Fatalf("Close() = %v, want bounded kill-wait timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("Close() took %v, want bounded wait", elapsed)
	}
	if !cancelled {
		t.Fatal("cancel was not called on forced close")
	}
}

func TestCommandConnNetConnMethods(t *testing.T) {
	t.Parallel()

	conn := &commandConn{}
	if got := conn.LocalAddr().Network(); got != "ssh" {
		t.Fatalf("LocalAddr().Network() = %q, want ssh", got)
	}
	if got := conn.LocalAddr().String(); got != "local" {
		t.Fatalf("LocalAddr().String() = %q, want local", got)
	}
	if got := conn.RemoteAddr().String(); got != "remote" {
		t.Fatalf("RemoteAddr().String() = %q, want remote", got)
	}
	if err := conn.SetDeadline(time.Now()); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("SetDeadline() = %v, want os.ErrInvalid", err)
	}
	if err := conn.SetReadDeadline(time.Now()); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("SetReadDeadline() = %v, want os.ErrInvalid", err)
	}
	if err := conn.SetWriteDeadline(time.Now()); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("SetWriteDeadline() = %v, want os.ErrInvalid", err)
	}
}

func TestAcceptableWaitErr(t *testing.T) {
	t.Parallel()

	if err := acceptableWaitErr(nil); err != nil {
		t.Fatalf("acceptableWaitErr(nil) = %v, want nil", err)
	}
	exitErr := exec.Command("sh", "-c", "exit 7").Run()
	if err := acceptableWaitErr(exitErr); err != nil {
		t.Fatalf("acceptableWaitErr(exitErr) = %v, want nil", err)
	}
	plainErr := errors.New("wait failed")
	if err := acceptableWaitErr(plainErr); !errors.Is(err, plainErr) {
		t.Fatalf("acceptableWaitErr(plainErr) = %v, want plainErr", err)
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }

func (nopWriteCloser) Close() error { return nil }

type nopReadCloser struct{}

func (nopReadCloser) Read([]byte) (int, error) { return 0, errors.New("unexpected read") }

func (nopReadCloser) Close() error { return nil }

func closedWait(err error) <-chan error {
	ch := make(chan error, 1)
	ch <- err
	return ch
}

type unixSocketDialer struct{}

func (unixSocketDialer) Dial(ctx context.Context, host config.Host) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", host.SocketPath)
}

type staticDialer struct {
	conn net.Conn
}

func (d staticDialer) Dial(context.Context, config.Host) (net.Conn, error) {
	return d.conn, nil
}

type countingDialer struct {
	conn  net.Conn
	calls int
}

func (d *countingDialer) Dial(context.Context, config.Host) (net.Conn, error) {
	d.calls++
	return d.conn, nil
}

type errorDialer struct {
	err error
}

func (d errorDialer) Dial(context.Context, config.Host) (net.Conn, error) {
	return nil, d.err
}

func startRemoteEchoSocket(t *testing.T) string {
	t.Helper()

	socketPath := filepath.Join(t.TempDir(), "remote.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := proto.NewReader(conn)
		writer := proto.NewWriter(conn)
		msg, err := reader.ReadMsg()
		if err != nil {
			return
		}
		_ = writer.WriteMsg(&proto.Message{Type: proto.MsgTypeNotify, Text: "echo:" + msg.Session})
	}()

	return socketPath
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readEventually(t *testing.T, path, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return string(data)
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", path, err)
			}
			t.Fatalf("file %q did not contain %q:\n%s", path, want, data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var _ Dialer = unixSocketDialer{}
