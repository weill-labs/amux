# Hot Reload Design

## Overview

When the amux binary is rebuilt, the running client detects the change and replaces itself via `syscall.Exec`. The new process reconnects to the existing server, receives the layout snapshot and pane content, and renders. The server is unaware anything happened.

This builds on the client-side rendering architecture (LAB-87) where the server sends raw data (`MsgPaneOutput` + `MsgLayout`) and each client renders independently.

## Trigger Mechanisms

1. **File watch**: `fsnotify` watches the resolved path of `os.Executable()`. On write/create events, debounce 200ms (builds write the binary multiple times), then exec.
2. **Keybinding**: `Ctrl-a r` triggers an immediate exec.

## Reload Sequence

```
1. Pre-validate: stat the binary, verify it's executable
2. Send MsgDetach to server (clean disconnect)
3. Close connection to server
4. Restore terminal state (cooked mode)
5. Exit alt screen buffer
6. syscall.Exec(execPath, os.Args, os.Environ())
   ── process replaced (goroutines cease to exist, no cleanup needed) ──
7. New binary starts, enters runMux()
8. Enters alt screen + raw mode
9. Connects to existing server socket
10. Server sends MsgLayout + MsgPaneOutput (standard attach flow)
11. Client renders — session continues
```

Total visible disruption: one frame of flicker (alt screen exit then re-enter).

Note: `syscall.Exec` atomically replaces the entire process image. All goroutines (SIGWINCH handler, message reader, render loop, stdin reader) simply cease to exist. Explicit goroutine cleanup is unnecessary and would add latency and deadlock risk.

Note: Any PTY output occurring during the ~1-frame disconnect gap is invisible to the new client. However, the server's pane emulators still process it, so the reattach snapshot includes this output. No data is lost.

## Implementation

### New file: `reload.go`

**Executable path**: Resolve `os.Executable()` + `filepath.EvalSymlinks()` once at startup. Store the resolved path. Use it for both the file watcher and the exec call (avoids TOCTOU issues if the binary is replaced between watch detection and exec).

**`watchBinary(execPath string, triggerReload chan struct{})` goroutine**:
- Watch the binary's parent directory (not the file directly — watching a file misses replacements where the old inode is removed and a new one created)
- On Write/Create events matching the binary name, reset a 200ms debounce timer
- When debounce fires, send on `triggerReload` channel

**`execSelf(execPath string, fd int, oldState *term.State)` function**:
- Pre-validate: `os.Stat(execPath)` — if binary doesn't exist or isn't accessible, log error and return without tearing down the connection. This handles the most common failure modes (file not found, permissions) before any destructive action.
- Send `MsgDetach` to server for clean disconnect
- Close the server connection
- Restore terminal: `term.Restore(fd, oldState)`
- Exit alt screen: write `AltScreenExit` + `ResetTitle`
- `syscall.Exec(execPath, os.Args, os.Environ())`
- If exec fails (unlikely after pre-validation): this is an unrecoverable state — the connection is already closed. Print error to stderr and `os.Exit(1)`. The user can restart `amux` manually.

### Modified file: `main.go`

**Keybinding**: Add `case 'r':` in the Ctrl-a prefix handler that sends on `triggerReload`.

**Watcher startup**: In `runMux()`, resolve executable path once, start `watchBinary()` goroutine, select on `triggerReload` channel alongside existing done channel.

**Reload handling**: When `triggerReload` fires, call `execSelf(execPath, fd, oldState)`.

### File placement

`reload.go` lives at the package root (alongside `main.go` and `client.go`) because it needs access to `runMux()`'s local state: the connection, terminal fd, and saved terminal state. This is the same pattern as `client.go`.

## Edge Cases

- **Binary deleted during rebuild**: fsnotify fires Remove then Create. The 200ms debounce absorbs this. Exec only fires after events settle.
- **Pre-validation catches bad binary**: If the binary doesn't exist or isn't executable after debounce, `execSelf` logs and returns without tearing down. Client continues running.
- **Exec fails after pre-validation** (corrupt binary, rare): Connection is already closed. Print error and exit. User restarts manually.
- **Server died during reload**: Normal behavior. New process tries to connect, fails, starts a new server. Same as fresh `amux` invocation.
- **Multiple rapid rebuilds**: Debounce timer resets on each event. Only the final stable binary triggers exec.
- **Binary path is a symlink** (e.g., `~/.local/bin/amux` -> build output): Resolved once at startup via `filepath.EvalSymlinks`. Watcher uses the resolved path.

## Dependencies

- `github.com/fsnotify/fsnotify` — file system event watcher (well-maintained, standard choice)

## Testing

- Integration test: start amux, type text, send `Ctrl-a r`, verify session continues (text still visible, status bar present). For binary change detection: rebuild the binary during the test, verify auto-reload via session continuity.
- Unit test: debounce logic (timer reset behavior)
- Manual verification: `Ctrl-a r` triggers reload, `go build` triggers auto-reload
