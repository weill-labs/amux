# SSH Session Takeover — Design Spec

## Problem

When a user SSH's into a remote machine from an amux pane and runs `amux`, they get a nested amux-inside-amux. This is confusing and defeats the purpose of a unified local view. The user wants the remote panes to appear as first-class citizens in their local layout.

## User Experience

```
1. User is in local amux with pane-1
2. Types: ssh lambda-a100
3. On remote: types amux
4. Local amux detects the nested session
5. pane-1 is replaced with the remote's panes (splice)
6. Remote panes appear in the local layout with @lambda-a100 host labels
7. Connection status indicators show ⚡ connected
```

No extra commands. No configuration. Just SSH + amux = remote panes in local grid.

## Architecture

```
LOCAL                           REMOTE (lambda-a100)
┌─────────────┐                ┌─────────────┐
│ pane-1      │    SSH PTY     │             │
│ $ ssh lamb..│───────────────▶│ $ amux      │
│             │                │             │
│ readLoop()  │◀── \x1b]999;  │ emits       │
│ detects     │    takeover    │ takeover    │
│ sequence    │    sequence    │ sequence    │
└──────┬──────┘                └──────┬──────┘
       │                              │
       ▼                              ▼
  Local server                  Remote amux server
  converts pane-1               starts with session
  into proxy panes              name "main@macbook"
  via remote manager            creates panes
```

## Detection: Custom Escape Sequence

When `amux` starts and detects it's running inside another amux pane (the PTY already exists — it's the SSH session), it emits a **takeover request** through the terminal:

```
\x1b]999;amux-takeover;<json-payload>\x07
```

Payload:
```json
{
  "session": "main@macbook",
  "socket": "/tmp/amux-1000/main@macbook",
  "host": "lambda-a100",
  "uid": "1000",
  "panes": [
    {"id": 1, "name": "pane-1", "cols": 80, "rows": 24},
    {"id": 2, "name": "pane-2", "cols": 80, "rows": 24}
  ]
}
```

The local amux's `readLoop()` already scans PTY output (for OSC 52). A new scanner detects the `\x1b]999;amux-takeover;` prefix, parses the JSON, and triggers the takeover.

### How the Remote Detects It's Nested

When `amux` starts (either as client or server), it checks:
1. Is `AMUX_PANE` set? (Same-host nesting — always set in amux pane shells — skip takeover)
2. Is `TERM=amux`? (amux pane shells set `TERM=amux`; SSH forwards `TERM` by default, so this reliably indicates the SSH session originated from an amux pane)
3. Is `SSH_CONNECTION` set? (Confirms this is an SSH session)
4. If all three conditions are met (`SSH_CONNECTION` set, `TERM=amux`, `AMUX_PANE` not set), emit the takeover sequence.

The remote `amux` emits the takeover sequence, then **waits** for an acknowledgment before proceeding. If no ack arrives within 2 seconds (e.g., running outside amux, or in a terminal that doesn't understand the sequence), it falls back to normal behavior (standalone session).

### Acknowledgment

The local amux sends back through the PTY input (stdin of the SSH session):

```
\x1b]999;amux-takeover-ack\x07
```

The remote `amux` reads this from stdin, confirms takeover mode, and enters a "managed" mode where it acts as a thin server for the local amux to connect to.

## Splice: Replacing the SSH Pane

Once the local server receives the takeover request:

1. **Pause the pane's readLoop** — stop feeding PTY output to clients
2. **Establish the remote connection** — use the existing `remote.Manager` to SSH to the host and connect to the remote amux socket (reusing the existing SSH connection if possible, or opening a new one)
3. **Query remote layout** — get the list of panes from the remote server
4. **Replace the local pane** — remove pane-1 from the layout tree and splice in N proxy panes, one per remote pane
5. **Start proxying** — each proxy pane routes I/O through the remote manager

### Layout Splice

If pane-1 occupied a cell of size W×H in the layout tree, and the remote has 2 panes in a vertical split:

**Before:**
```
┌──────────────┬──────────────┐
│   pane-1     │   pane-2     │
│  (SSH)       │  (local)     │
└──────────────┴──────────────┘
```

**After:**
```
┌───────┬──────┬──────────────┐
│ rpane1│rpane2│   pane-2     │
│ @lamb │@lamb │  (local)     │
└───────┴──────┴──────────────┘
```

The splice replaces the leaf cell with the remote's layout subtree, preserving the remote's split structure.

### What Happens to the SSH PTY

The original SSH PTY (pane-1's `ptmx`) is **kept alive but dormant**. It maintains the SSH connection. The remote manager uses a separate SSH connection (via `golang.org/x/crypto/ssh`) for the amux wire protocol. If the wire protocol connection drops, the original PTY is still there as a fallback — the user can `amux unsplice` to go back to the raw SSH session.

Alternative: use the existing SSH PTY as the transport (multiplex amux protocol over the PTY). This avoids a second SSH connection but is more complex. **Recommend: separate connection for simplicity.**

## Implementation Phases

### Phase 1: Takeover Sequence Detection
1. Add `AmuxControlScanner` to `internal/mux/` (similar to `OSC52Scanner`)
2. Add `onTakeover` callback to `Pane` struct
3. Wire scanner into `readLoop()` after OSC 52 scanning
4. Parse JSON payload, invoke callback

### Phase 2: Remote amux Emission
5. In `main.go` startup, detect nested context (`SSH_CONNECTION` + not already managed)
6. Emit takeover sequence with session info to stdout
7. Wait for ack on stdin with 2s timeout
8. If acked: enter managed mode (server only, no local UI)
9. If no ack: proceed normally (standalone amux)

### Phase 3: Splice Logic
10. Add `SplicePane(oldPaneID, newSubtree)` to `Window`
11. On takeover callback: establish remote connection, query panes
12. Build proxy panes for each remote pane
13. Splice into layout tree, replacing the SSH pane
14. Broadcast layout update

### Phase 4: Unsplice / Cleanup
15. Add `amux unsplice <host>` command — reverts to raw SSH pane
16. Handle remote disconnect gracefully (revert to frozen SSH pane)
17. Handle the SSH pane's shell exiting (clean up proxy panes)

## Edge Cases

- **Remote has no panes yet** — the remote `amux` just started, so it creates a default pane. The splice gets 1 pane.
- **Remote amux is already running** — `amux` reattaches to existing session. Splice gets N existing panes.
- **Multiple SSH hops** — user SSH's through a jump host. The takeover sequence passes through transparently (it's just terminal output). Only the first amux in the chain detects it.
- **User runs amux on remote without SSH** — `SSH_CONNECTION` is not set, so no takeover sequence is emitted. Normal standalone behavior.
- **Local amux is not running** — no scanner to detect the sequence. The terminal ignores the escape sequence (rendered as garbage briefly, then cleared by the remote amux's TUI).

## New Types

```go
// TakeoverRequest is emitted by a nested amux through the PTY.
type TakeoverRequest struct {
    Session string          `json:"session"`
    Socket  string          `json:"socket"`
    Host    string          `json:"host"`
    UID     string          `json:"uid"`
    Panes   []TakeoverPane  `json:"panes"`
}

type TakeoverPane struct {
    ID   uint32 `json:"id"`
    Name string `json:"name"`
    Cols int    `json:"cols"`
    Rows int    `json:"rows"`
}
```

## Open Questions

1. **Should the original SSH pane be preserved as a hidden "escape hatch"?** If yes, `unsplice` restores it. If no, the SSH connection is transferred to the remote manager and the original PTY is closed.

2. **Should the remote amux enter a special "managed" mode** where it doesn't render a TUI (since the local amux handles rendering)? This would save the remote from doing unnecessary compositing.

3. **Can we reuse the SSH PTY as the transport** instead of opening a second SSH connection? This would be more efficient but requires multiplexing the amux wire protocol over the PTY byte stream (mixed with shell output until takeover completes).
