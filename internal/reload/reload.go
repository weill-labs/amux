// Package reload provides binary hot-reload support.
// It watches the amux binary for changes and triggers reload signals.
package reload

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	reloadDebounceDelay   = 200 * time.Millisecond
	reloadReadyRetryDelay = 50 * time.Millisecond
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

func executablePrefixLooksValid(prefix []byte) bool {
	if len(prefix) >= 2 && prefix[0] == '#' && prefix[1] == '!' {
		return true
	}
	if len(prefix) < 4 {
		return false
	}

	switch {
	case prefix[0] == 0x7f && prefix[1] == 'E' && prefix[2] == 'L' && prefix[3] == 'F':
		return true
	case prefix[0] == 0xfe && prefix[1] == 0xed && prefix[2] == 0xfa && prefix[3] == 0xce:
		return true
	case prefix[0] == 0xce && prefix[1] == 0xfa && prefix[2] == 0xed && prefix[3] == 0xfe:
		return true
	case prefix[0] == 0xfe && prefix[1] == 0xed && prefix[2] == 0xfa && prefix[3] == 0xcf:
		return true
	case prefix[0] == 0xcf && prefix[1] == 0xfa && prefix[2] == 0xed && prefix[3] == 0xfe:
		return true
	case prefix[0] == 0xca && prefix[1] == 0xfe && prefix[2] == 0xba && prefix[3] == 0xbe:
		return true
	case prefix[0] == 0xbe && prefix[1] == 0xba && prefix[2] == 0xfe && prefix[3] == 0xca:
		return true
	case prefix[0] == 0xca && prefix[1] == 0xfe && prefix[2] == 0xba && prefix[3] == 0xbf:
		return true
	case prefix[0] == 0xbf && prefix[1] == 0xba && prefix[2] == 0xfe && prefix[3] == 0xca:
		return true
	default:
		return false
	}
}

func executablePathReady(execPath string) bool {
	info, err := os.Stat(execPath)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false
	}

	f, err := os.Open(execPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var prefix [4]byte
	n, err := io.ReadFull(f, prefix[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false
	}
	return executablePrefixLooksValid(prefix[:n])
}

// WatchBinary watches for changes to the binary at execPath and sends on
// triggerReload when a change is detected.
// If ready is non-nil, it is closed after the file watcher is registered.
func WatchBinary(execPath string, triggerReload chan<- struct{}, ready chan<- struct{}) {
	watchBinary(execPath, triggerReload, ready, nil)
}

func watchBinary(execPath string, triggerReload chan<- struct{}, ready chan<- struct{}, stop <-chan struct{}) {
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
		case <-stop:
			return

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !watchEventMatchesTarget(event, base, matchChmod) {
				continue
			}
			debounce = resetDebounceTimer(debounce, reloadDebounceDelay)
			debounceC = debounce.C

		case <-debounceC:
			debounceC = nil
			if drainPendingReloadEvents(watcher.Events, watcher.Errors, base, matchChmod) {
				debounce = resetDebounceTimer(debounce, reloadDebounceDelay)
				debounceC = debounce.C
				continue
			}
			if !executablePathReady(execPath) {
				debounce = resetDebounceTimer(debounce, reloadReadyRetryDelay)
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
