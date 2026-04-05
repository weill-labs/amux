// Package reload provides binary hot-reload support.
// It watches the amux binary for changes and triggers reload signals.
package reload

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ResolveExecutable returns the absolute invocation path of the running binary.
func ResolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return NormalizeExecutablePath(exe)
}

// NormalizeExecutablePath returns an absolute executable path without
// collapsing the invocation symlink. `make install` replaces the invoked path,
// so preserving it keeps auto-reload pointed at the file that actually changes.
func NormalizeExecutablePath(exe string) (string, error) {
	if exe == "" {
		return "", fmt.Errorf("empty executable path")
	}
	if !filepath.IsAbs(exe) {
		abs, err := filepath.Abs(exe)
		if err != nil {
			return "", err
		}
		exe = abs
	}
	exe = filepath.Clean(exe)
	if _, err := os.Stat(exe); err != nil {
		return "", err
	}
	return exe, nil
}

func resetDebounceTimer(timer *time.Timer, delay time.Duration) *time.Timer {
	if timer == nil {
		return time.NewTimer(delay)
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
	return timer
}

func watchEventMatchesTarget(event fsnotify.Event, base string, matchChmod bool) bool {
	if filepath.Base(event.Name) != base {
		return false
	}

	mask := fsnotify.Write | fsnotify.Create | fsnotify.Rename
	if matchChmod {
		mask |= fsnotify.Chmod
	}
	return event.Op&mask != 0
}

func drainPendingReloadEvents(events <-chan fsnotify.Event, errors <-chan error, base string, matchChmod bool) bool {
	drained := false
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return drained
			}
			if watchEventMatchesTarget(event, base, matchChmod) {
				drained = true
			}
		case _, ok := <-errors:
			if !ok {
				return drained
			}
		default:
			return drained
		}
	}
}

// WatchBinary watches for changes to the binary at execPath and sends on
// triggerReload when a change is detected (with 200ms debounce).
// If ready is non-nil, it is closed after the file watcher is registered.
func WatchBinary(execPath string, triggerReload chan<- struct{}, ready chan<- struct{}) {
	dir := filepath.Dir(execPath)
	base := filepath.Base(execPath)
	matchChmod := false
	if info, err := os.Lstat(execPath); err == nil {
		matchChmod = info.Mode()&os.ModeSymlink != 0
	}

	var signalReady sync.Once
	closeReady := func() {
		if ready != nil {
			signalReady.Do(func() { close(ready) })
		}
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		closeReady()
		return
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		closeReady()
		return
	}

	closeReady()

	var debounce *time.Timer
	var debounceC <-chan time.Time

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !watchEventMatchesTarget(event, base, matchChmod) {
				continue
			}
			debounce = resetDebounceTimer(debounce, 200*time.Millisecond)
			debounceC = debounce.C

		case <-debounceC:
			debounceC = nil
			if drainPendingReloadEvents(watcher.Events, watcher.Errors, base, matchChmod) {
				debounce = resetDebounceTimer(debounce, 200*time.Millisecond)
				debounceC = debounce.C
				continue
			}
			select {
			case triggerReload <- struct{}{}:
			default:
			}

		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}
