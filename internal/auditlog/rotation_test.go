package auditlog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDefaultRotationOptionsBoundsTotalToHundredMiB(t *testing.T) {
	t.Parallel()

	opts := DefaultRotationOptions()
	if got, want := opts.MaxBytes*int64(opts.MaxBackups+1), int64(100<<20); got != want {
		t.Fatalf("default total log budget = %d, want %d", got, want)
	}
}

func TestRotatingFileWriterBoundsTotalLogBytes(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "main.log")
	const maxBytes int64 = 12
	w, err := NewRotatingFileWriter(logPath, RotationOptions{
		MaxBytes:   maxBytes,
		MaxBackups: 2,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}

	for i := 0; i < 8; i++ {
		if _, err := fmt.Fprintf(w, "entry-%02d\n", i); err != nil {
			t.Fatalf("Write entry %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	total := totalRegularFileSize(t, logPath, logPath+".1", logPath+".2")
	if total > maxBytes*3 {
		t.Fatalf("total rotated log size = %d, want <= %d", total, maxBytes*3)
	}
	if _, err := os.Stat(logPath + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup stat error = %v", err)
	}
}

func TestRotatingFileWriterRotatesExistingOversizedLog(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "main.log")
	if err := os.WriteFile(logPath, []byte("existing-log"), 0o600); err != nil {
		t.Fatalf("WriteFile existing log: %v", err)
	}

	w, err := NewRotatingFileWriter(logPath, RotationOptions{
		MaxBytes:   8,
		MaxBackups: 1,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	if _, err := w.Write([]byte("new\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertFileContains(t, logPath, "new\n")
	assertFileContains(t, logPath+".1", "existing-log")
}

func TestRotatingFileWriterWithNoBackupsKeepsOnlyActiveLog(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "main.log")
	w, err := NewRotatingFileWriter(logPath, RotationOptions{
		MaxBytes:   5,
		MaxBackups: 0,
	})
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	if _, err := w.Write([]byte("abcdefghi")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if total := totalRegularFileSize(t, logPath); total > 5 {
		t.Fatalf("active log size = %d, want <= 5", total)
	}
	if _, err := os.Stat(logPath + ".1"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup stat error = %v", err)
	}
}

func TestRotatingFileWriterRejectsWriteAfterClose(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "main.log")
	w, err := NewRotatingFileWriter(logPath, RotationOptions{MaxBytes: 8})
	if err != nil {
		t.Fatalf("NewRotatingFileWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.Write([]byte("after close")); !os.IsNotExist(err) && err != os.ErrClosed {
		t.Fatalf("Write after Close error = %v, want os.ErrClosed", err)
	}
}

func TestInstallProcessLogRotationWritesFileAndForegroundStderr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "main.log")
	readPipe, writePipe, restore := redirectStderrToPipe(t)
	defer readPipe.Close()
	defer writePipe.Close()
	defer restore()

	writer, cleanup, err := InstallProcessLogRotation(logPath, RotationOptions{MaxBytes: 128, MaxBackups: 1})
	if err != nil {
		t.Fatalf("InstallProcessLogRotation: %v", err)
	}
	if _, err := writer.Write([]byte("foreground log\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cleanup()
	restore()
	writePipe.Close()

	tee, err := io.ReadAll(readPipe)
	if err != nil {
		t.Fatalf("ReadAll tee: %v", err)
	}
	if !strings.Contains(string(tee), "foreground log") {
		t.Fatalf("foreground stderr tee = %q, want log line", tee)
	}
	assertFileContains(t, logPath, "foreground log")
}

func TestInstallProcessLogRotationDoesNotTeeRegularStderr(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "main.log")
	stderrPath, restore := redirectStderrToRegularFile(t)
	defer restore()

	writer, cleanup, err := InstallProcessLogRotation(logPath, RotationOptions{MaxBytes: 128, MaxBackups: 1})
	if err != nil {
		t.Fatalf("InstallProcessLogRotation: %v", err)
	}
	if _, err := writer.Write([]byte("daemon log\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	cleanup()
	restore()

	assertFileContains(t, logPath, "daemon log")
	data, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("ReadFile stderr: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("regular stderr duplicate = %q, want empty", data)
	}
}

func totalRegularFileSize(t *testing.T, paths ...string) int64 {
	t.Helper()

	var total int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("%q mode = %s, want regular file", path, info.Mode())
		}
		total += info.Size()
	}
	return total
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%q = %q, want substring %q", path, data, want)
	}
}

func redirectStderrToPipe(t *testing.T) (*os.File, *os.File, func()) {
	t.Helper()

	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	restore := redirectStderrToFile(t, writePipe)
	return readPipe, writePipe, restore
}

func redirectStderrToRegularFile(t *testing.T) (string, func()) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stderr.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("OpenFile stderr: %v", err)
	}
	restore := redirectStderrToFile(t, file)
	var closed bool
	return path, func() {
		if closed {
			return
		}
		closed = true
		restore()
		if err := file.Close(); err != nil {
			t.Fatalf("Close stderr file: %v", err)
		}
	}
}

func redirectStderrToFile(t *testing.T, file *os.File) func() {
	t.Helper()

	oldStderr, err := unix.Dup(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatalf("Dup stderr: %v", err)
	}
	if err := unix.Dup2(int(file.Fd()), int(os.Stderr.Fd())); err != nil {
		_ = unix.Close(oldStderr)
		t.Fatalf("Dup2 stderr: %v", err)
	}

	var restored bool
	return func() {
		if restored {
			return
		}
		restored = true
		if err := unix.Dup2(oldStderr, int(os.Stderr.Fd())); err != nil {
			_ = unix.Close(oldStderr)
			t.Fatalf("restore stderr: %v", err)
		}
		if err := unix.Close(oldStderr); err != nil {
			t.Fatalf("close old stderr: %v", err)
		}
	}
}
