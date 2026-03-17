// Package reload provides binary hot-reload support.
// It watches the amux binary for changes and triggers reload signals.
package reload

import (
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ResolveExecutable returns the resolved absolute path of the running binary.
func ResolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// WatchBinary watches for changes to the binary at execPath and sends on
// triggerReload when a change is detected (with 200ms debounce).
// If ready is non-nil, it is closed after the file watcher is registered.
func WatchBinary(execPath string, triggerReload chan<- struct{}, ready chan<- struct{}) {
	dir := filepath.Dir(execPath)
	base := filepath.Base(execPath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if ready != nil {
			close(ready)
		}
		return
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		if ready != nil {
			close(ready)
		}
		return
	}

	if ready != nil {
		close(ready)
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
