package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func sessionLockPath(sessionName string) string {
	return filepath.Join(SocketDir(), sessionName+".lock")
}

func acquireSessionLock(sessionName string) (*os.File, error) {
	if err := os.MkdirAll(SocketDir(), 0700); err != nil {
		return nil, fmt.Errorf("creating socket dir: %w", err)
	}
	lockFile, err := os.OpenFile(sessionLockPath(sessionName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening session lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("server already running for session %q", sessionName)
		}
		return nil, fmt.Errorf("locking session: %w", err)
	}
	return lockFile, nil
}

func restoreOrAcquireSessionLock(sessionName string, fd int) (*os.File, error) {
	if fd <= 0 {
		return acquireSessionLock(sessionName)
	}
	lockFile := os.NewFile(uintptr(fd), "session-lock")
	if lockFile == nil {
		return nil, fmt.Errorf("invalid session lock fd %d", fd)
	}
	syscall.CloseOnExec(fd)
	return lockFile, nil
}

func closeSessionLock(lockFile *os.File) {
	if lockFile != nil {
		_ = lockFile.Close()
	}
}
