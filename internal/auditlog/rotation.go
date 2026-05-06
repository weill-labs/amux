package auditlog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	defaultMaxLogBytes = 20 << 20
	defaultLogBackups  = 4
)

// RotationOptions controls size-based log rotation.
type RotationOptions struct {
	MaxBytes   int64
	MaxBackups int
}

// DefaultRotationOptions keeps the active log plus four backups bounded to
// roughly 100 MiB total.
func DefaultRotationOptions() RotationOptions {
	return RotationOptions{
		MaxBytes:   defaultMaxLogBytes,
		MaxBackups: defaultLogBackups,
	}
}

// NewRotatingFileWriter opens path for append and rotates it by size.
func NewRotatingFileWriter(path string, opts RotationOptions) (io.WriteCloser, error) {
	opts = normalizeRotationOptions(opts)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	w := &rotatingFileWriter{
		path:       path,
		maxBytes:   opts.MaxBytes,
		maxBackups: opts.MaxBackups,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

// InstallProcessLogRotation returns a synchronous logger writer backed by a
// rotating file. Foreground invocations keep receiving stderr output, but
// inherited regular log files are not teed because that would recreate the
// unbounded file this rotation avoids.
func InstallProcessLogRotation(path string, opts RotationOptions) (io.Writer, func(), error) {
	MarkSocketLogFDsCloseOnExec(filepath.Dir(path))
	writer, err := NewRotatingFileWriter(path, opts)
	if err != nil {
		return nil, nil, err
	}

	teeFile := teeFileForFD(int(os.Stderr.Fd()))
	logWriter := &lockedWriter{w: writer}
	if teeFile != nil {
		logWriter.w = io.MultiWriter(writer, teeFile)
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = writer.Close()
			closeTeeFile(teeFile)
		})
	}
	return logWriter, cleanup, nil
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func teeFileForFD(fd int) *os.File {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFREG {
		return nil
	}
	teeFD, err := unix.Dup(fd)
	if err != nil {
		return nil
	}
	unix.CloseOnExec(teeFD)
	return os.NewFile(uintptr(teeFD), "amux-log-stderr")
}

func MarkSocketLogFDsCloseOnExec(logDir string) {
	if logDir == "" {
		return
	}
	fdDir := "/proc/self/fd"
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil || fd <= 2 {
			continue
		}
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		if isSocketLogFDTarget(logDir, target) {
			unix.CloseOnExec(fd)
		}
	}
}

func isSocketLogFDTarget(logDir, target string) bool {
	cleanDir := filepath.Clean(logDir)
	target = strings.TrimSuffix(target, " (deleted)")
	if filepath.Dir(target) != cleanDir {
		return false
	}
	return strings.Contains(filepath.Base(target), ".log")
}

func closeTeeFile(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

type rotatingFileWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

func normalizeRotationOptions(opts RotationOptions) RotationOptions {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxLogBytes
	}
	if opts.MaxBackups < 0 {
		opts.MaxBackups = defaultLogBackups
	}
	return opts
}

func (w *rotatingFileWriter) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	if w.size >= w.maxBytes {
		return w.rotate()
	}
	return nil
}

func (w *rotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	written := 0
	for len(p) > 0 {
		if w.file == nil {
			return written, os.ErrClosed
		}
		if w.size >= w.maxBytes {
			if err := w.rotate(); err != nil {
				return written, err
			}
		}
		space := w.maxBytes - w.size
		if space <= 0 {
			if err := w.rotate(); err != nil {
				return written, err
			}
			space = w.maxBytes
		}

		chunk := len(p)
		if int64(chunk) > space {
			chunk = int(space)
		}
		n, err := w.file.Write(p[:chunk])
		w.size += int64(n)
		written += n
		p = p[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *rotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingFileWriter) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	if w.maxBackups <= 0 {
		if err := os.Remove(w.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		oldest := backupPath(w.path, w.maxBackups)
		if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
			return err
		}
		for i := w.maxBackups - 1; i >= 1; i-- {
			src := backupPath(w.path, i)
			dst := backupPath(w.path, i+1)
			if err := renameIfExists(src, dst); err != nil {
				return err
			}
		}
		if err := renameIfExists(w.path, backupPath(w.path, 1)); err != nil {
			return err
		}
	}

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.size = 0
	return nil
}

func backupPath(path string, n int) string {
	return fmt.Sprintf("%s.%d", path, n)
}

func renameIfExists(src, dst string) error {
	if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
