// Package reload provides binary hot-reload support.
// It watches the amux binary for changes and triggers reload signals.
package reload

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// ShouldWatchBinary reports whether hot reload should watch execPath from the
// current working directory context. Installed shared binaries only watch when
// launched from the checkout that last installed them.
func ShouldWatchBinary(execPath string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return true
	}
	return shouldWatchBinary(execPath, cwd)
}

func shouldWatchBinary(execPath, cwd string) bool {
	sourceRepo, ok := readInstallSourceRepo(execPath)
	if !ok {
		return true
	}

	repoRoot, ok := findRepoRoot(cwd)
	if !ok {
		return false
	}

	sourceRepo = cleanPath(sourceRepo)
	repoRoot = cleanPath(repoRoot)
	return sourceRepo != "" && repoRoot == sourceRepo
}

func readInstallSourceRepo(execPath string) (string, bool) {
	f, err := os.Open(execPath + ".install-meta")
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "source_repo=") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "source_repo=")), true
	}
	return "", false
}

func findRepoRoot(start string) (string, bool) {
	dir := cleanPath(start)
	for dir != "" && dir != string(filepath.Separator) {
		gitPath := filepath.Join(dir, ".git")
		if _, err := os.Stat(gitPath); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// WatchBinary watches for changes to the binary at execPath and sends on
// triggerReload when a change is detected (with 200ms debounce).
// If ready is non-nil, it is closed after the file watcher is registered.
func WatchBinary(execPath string, triggerReload chan<- struct{}, ready chan<- struct{}) {
	dir := filepath.Dir(execPath)
	base := filepath.Base(execPath)

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
