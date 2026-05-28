package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/weill-labs/amux/internal/config"
	"github.com/weill-labs/amux/internal/proto"
)

type Dialer interface {
	Dial(context.Context, config.Host) (net.Conn, error)
}

type Link struct {
	host   config.Host
	dialer Dialer

	mu     sync.Mutex
	conn   net.Conn
	reader *proto.Reader
	writer *proto.Writer
}

func NewLink(host config.Host, dialer Dialer) *Link {
	if dialer == nil {
		dialer = SSHDialer{}
	}
	return &Link{host: host, dialer: dialer}
}

func (l *Link) Connect(ctx context.Context) error {
	if l == nil {
		return errors.New("remote link is nil")
	}
	conn, err := l.dialer.Dial(ctx, l.host)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn != nil {
		_ = conn.Close()
		return errors.New("remote link already connected")
	}
	l.conn = conn
	l.reader = proto.NewReader(conn)
	l.writer = proto.NewWriter(conn)
	return nil
}

func (l *Link) ReadMsg() (*proto.Message, error) {
	l.mu.Lock()
	reader := l.reader
	l.mu.Unlock()
	if reader == nil {
		return nil, net.ErrClosed
	}
	return reader.ReadMsg()
}

func (l *Link) WriteMsg(msg *proto.Message) error {
	l.mu.Lock()
	writer := l.writer
	l.mu.Unlock()
	if writer == nil {
		return net.ErrClosed
	}
	return writer.WriteMsg(msg)
}

func (l *Link) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	conn := l.conn
	l.conn = nil
	l.reader = nil
	l.writer = nil
	l.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    5,
		InitialBackoff: time.Second,
		MaxBackoff:     30 * time.Second,
	}
}

func (p RetryPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	initial := p.InitialBackoff
	if initial <= 0 {
		initial = time.Second
	}
	maxDelay := p.MaxBackoff
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}

	delay := initial
	for i := 1; i < attempt; i++ {
		if delay >= maxDelay/2 {
			return maxDelay
		}
		delay *= 2
	}
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

type SSHDialer struct{}

func (SSHDialer) Dial(ctx context.Context, host config.Host) (net.Conn, error) {
	if host.SSH == "" {
		return nil, errors.New("ssh target is required")
	}
	if host.SocketPath == "" {
		return nil, errors.New("socket path is required")
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "ssh",
		"-o", "BatchMode=yes",
		host.SSH,
		"--",
		"nc", "-U", host.SocketPath,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("opening ssh stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("opening ssh stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("starting ssh: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	return &commandConn{
		cmd:    cmd,
		cancel: cancel,
		stdin:  stdin,
		stdout: stdout,
		waitCh: waitCh,
	}, nil
}

type commandConn struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	stdout io.ReadCloser
	waitCh <-chan error

	closeOnce sync.Once
	closeErr  error
}

func (c *commandConn) Read(p []byte) (int, error) {
	return c.stdout.Read(p)
}

func (c *commandConn) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

func (c *commandConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		select {
		case err := <-c.waitCh:
			c.closeErr = acceptableWaitErr(err)
		case <-time.After(2 * time.Second):
			c.cancel()
			if c.cmd != nil && c.cmd.Process != nil {
				_ = c.cmd.Process.Kill()
			}
			c.closeErr = acceptableWaitErr(<-c.waitCh)
		}
		_ = c.stdout.Close()
	})
	return c.closeErr
}

func acceptableWaitErr(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func (c *commandConn) LocalAddr() net.Addr {
	return sshAddr("local")
}

func (c *commandConn) RemoteAddr() net.Addr {
	return sshAddr("remote")
}

func (c *commandConn) SetDeadline(time.Time) error {
	return os.ErrInvalid
}

func (c *commandConn) SetReadDeadline(time.Time) error {
	return os.ErrInvalid
}

func (c *commandConn) SetWriteDeadline(time.Time) error {
	return os.ErrInvalid
}

type sshAddr string

func (a sshAddr) Network() string { return "ssh" }

func (a sshAddr) String() string { return string(a) }
