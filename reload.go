package main

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/weill-labs/amux/internal/render"
	"github.com/weill-labs/amux/internal/server"
	"golang.org/x/term"
)

// resolveExecutable returns the resolved absolute path of the running binary.
func resolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// watchBinary watches for changes to the binary at execPath and sends on
// triggerReload when a change is detected (with 200ms debounce).
func watchBinary(execPath string, triggerReload chan<- struct{}) {
	dir := filepath.Dir(execPath)
	base := filepath.Base(execPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		return
	}

	var debounce *time.Timer

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != base {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			// Reset debounce timer
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(200*time.Millisecond, func() {
				select {
				case triggerReload <- struct{}{}:
				default:
				}
			})

		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// execSelf replaces the current process with the binary at execPath.
// Pre-validates the binary before tearing down the connection.
func execSelf(execPath string, conn net.Conn, fd int, oldState *term.State) {
	// Pre-validate: binary must exist and be accessible
	if _, err := os.Stat(execPath); err != nil {
		return
	}

	// Clean disconnect from server
	server.WriteMsg(conn, &server.Message{Type: server.MsgTypeDetach})
	conn.Close()

	// Restore terminal state
	term.Restore(fd, oldState)
	os.Stdout.Write([]byte(render.AltScreenExit))
	os.Stdout.Write([]byte(render.ResetTitle))

	// Replace process
	err := syscall.Exec(execPath, os.Args, os.Environ())
	if err != nil {
		// Unrecoverable — connection is closed
		os.Stderr.WriteString("amux: reload failed: " + err.Error() + "\n")
		os.Exit(1)
	}
}
