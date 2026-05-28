# amux federation: mirroring a remote pane in the local layout

Date: 2026-05-28. Supersedes an earlier **uncommitted** design draft (referred to below as "the original draft"; it was never checked in). Related: [LAB-1934](https://linear.app/weill-labs/issue/LAB-1934/delete-all-remote-logic) (the deleted predecessor), [PR #826](https://github.com/weill-labs/amux/pull/826) (tracer bullet), [PR #825](https://github.com/weill-labs/amux/pull/825) (two-server harness).

## Motivation

A user working in `pane-70` on `hetzner-xl` should be able to mirror `pane-1786` on `hetzner-1` into their layout *next to* `pane-70`. Input typed into the new pane forwards to the remote pane's PTY. Closing the new pane only detaches — the remote pane keeps running. The remote pane is unchanged in the eyes of any user attached to `hetzner-1`.

LAB-1934 deleted an earlier remote feature (~7000 LOC) that wrapped `ssh user@host` inside a local pane. The remote `amux` server was not involved. That design's hidden cost was the lifecycle/reconnect layer in `internal/remote/host_conn.go` (791 LOC), `manager.go` (554), `host_conn_events.go` (512) — exactly the layer that would need to be rebuilt for any cross-machine feature.

This spec is structurally different: **two `amux` servers federate**, the local server holds a mirror pane in its layout fed by the wire protocol, and the remote pane stays where it is.

## What changed since the original draft

The original draft was reviewed by four independent perspectives (skeptic, architect, UX, codex) and produced a single tracer-bullet ticket (LAB-1952) whose only purpose was to falsify the riskiest premise: that scoped per-pane subscription would tangle with the existing whole-session broadcast paths in `internal/server/`.

**The tracer bullet result (PR #826, merged 2026-05-28):**

| File | LOC added | Notes |
| --- | ---: | --- |
| `internal/proto/wire.go` | 4 | `MsgTypeListPanes`, `MsgTypeAttachPane` |
| `internal/server/server.go` | 50 | First-message dispatch |
| `internal/server/client_conn.go` | **70** | **Key number — well under the 300 LOC blocker threshold** |
| `internal/server/session_events_client.go` | 53 | Event-loop attach validation |
| `internal/server/session_events_pane.go` | 18 | Send/close scoped subscribers on pane exit |
| `internal/server/capture_forward.go` | 3 | Skip scoped subscribers as capture clients |
| Other | 11 | gob register, re-exports, round-trip tests |
| **Total production code** | **~209** | (plus 476 LOC of tests) |

The PR's own characterization: *"The per-pane subscription filter cleanly slots into the existing broadcast path. `Session.broadcastPaneOutputNow` did not need refactoring. This is a clean branch in existing fanout/dispatch functions, not an invasive refactor."*

**This evidence is the only reason v2 exists.** Without it, the design would have been abandoned in favor of `tmate` / view-only sharing.

## Design Principles

1. **Federation is amux-to-amux, not SSH-to-shell.** The local server talks to the remote server's wire protocol over an SSH tunnel. The remote pane is owned and managed by the remote `amux` server. The local server holds a *mirror* — no PTY, just an emulator fed by `MsgTypePaneOutput`.
2. **Restricted mode is a security boundary.** A federated client over an SSH tunnel inherits the local server's wire protocol surface. `MsgTypeAttachPane` enters a restricted connection mode that rejects every command except scoped input forwarding. Already implemented and tested in PR #826.
3. **Names over IDs (as the durable handle, not on the wire).** Pane IDs are session-monotonic counters, not stable across remote server restarts, so the *user-facing* handle and the persisted `RemoteRef` use the pane **name**. The wire `MsgTypeAttachPane` is **ID-only** (as shipped in PR #826 — `handleAttachPane` reads `msg.PaneID`). The two are reconciled by a resolve step: before every attach (initial and each reconnect) the `MirrorManager` runs `MsgTypeListPanes`, resolves the stored name to the remote's *current* pane ID, then sends `MsgTypeAttachPane{PaneID}`. This keeps the wire unchanged for attach while names remain the stable handle.
4. **One subcommand namespace.** Everything federation-related lives under `amux remote <subcommand>`, matching `git remote` conventions. No new top-level verbs.
5. **Letterbox before negotiate.** v1 ships with a single resize policy: the mirror renders at the remote's geometry; the local cell pads or crops. Explicit `amux remote resize` is the only way to change the remote pane's size, and it affects every client attached to it.
6. **Fail-stop for hard cases.** Reconnect/backoff, checkpoint restoration of mirrors, and signal forwarding are explicitly bounded for v1. When boundaries are crossed, the mirror dies cleanly with a banner — it does not silently corrupt or buffer.

## System Relationship

```
hetzner-xl                                    hetzner-1
┌────────────────────────────┐                ┌────────────────────────┐
│ amux server (local)        │                │ amux server (remote)   │
│ ┌────────────────────────┐ │                │ ┌────────────────────┐ │
│ │ pane-70 (local PTY)    │ │                │ │ pane-1786 (PTY)    │ │
│ │ pane-91 (mirror)       │◀┼─ PaneOutput ───┼─│   ↑ broadcast      │ │
│ │   writeOverride ───────┼─┼─ InputPane ───▶│ │   ↑ accept input   │ │
│ └────────────────────────┘ │                │ └────────────────────┘ │
└──────────────┬─────────────┘                └────────┬───────────────┘
               │                                       │
        local Unix socket                       local Unix socket
               │                                       │
               └────────── SSH -W tunnel ──────────────┘
```

The federated client is a **client stub running inside the local server process** (architect's framing). It does not render — the local client renders. It does not own the remote pane — the remote server does. It is the adapter between the wire protocol and the local mirror pane's emulator.

## Architecture

### Reused (already in `main`)

These survived LAB-1934 and LAB-1937 deletions and are load-bearing for v2:

- `mux.Pane.writeOverride` (`internal/mux/pane.go:110`) — input routing.
- `mux.NewProxyPaneWithScrollback` (`internal/mux/pane.go:808`) — creates a pane with no PTY.
- `mux.Pane.IsProxy()` (`internal/mux/pane.go:845`).
- `server.checkpoint.IsProxy` serialization (`internal/server/checkpoint.go:76`) — preserves proxy panes across server reload.
- `icons.RemoteHost` (field defined at `internal/render/icons.go:16`; rendered as the `@` glyph in the default themes, e.g. the Catppuccin-mocha literal at `:64`) — status line already renders `@<host>` for any pane with `PaneMeta.Host != ""`.
- **New from PR #826**: `MsgTypeListPanes`, `MsgTypeAttachPane`, restricted-mode dispatch in `client_conn.go`, scoped subscriber tracking in `session_events_pane.go`.

### Deleted and not coming back

- `internal/transport/ssh/` (1400 LOC), `internal/transport/mosh/` (stub) — v2 spawns a raw `ssh` child that execs `nc -U` against the remote Unix socket (see [Transport](#transport)), not a `Transport` abstraction.
- `internal/remote/host_conn.go`, `manager.go`, `host_conn_events.go` — the lifecycle layer that ballooned. v2 keeps lifecycle small enough to live in `internal/server/mirror/` (see below).
- `Hosts` map / `TransportConfig` in config — v2 introduces a smaller `[remote.hosts]` table (~80 LOC).

### New (this spec)

| Package / file | Purpose | LOC estimate |
| --- | --- | --: |
| `internal/config/config.go` | Restore minimal `Host` struct + `[remote.hosts]` TOML | 80 |
| `internal/remote/` (new) | `Link` (one SSH tunnel lifecycle), `Dialer` (interface for test bypass) | 400 |
| `internal/server/mirror/` (new) | `MirrorManager`: registry of all mirror panes; per-mirror state machine (connecting / connected / reconnecting / dead) | 500 |
| `internal/server/commands_remote.go` (new) | `remote add/list/rm/panes/attach/detach/resize` handlers | 250 |
| `internal/server/commands_layout.go` | `--attach <host>:<pane-name>` flag on `spawn` | 100 |
| `internal/client/chooser.go` | Extract `Source` interface; add `RemoteSource` (calls `remote panes` via local server) | 150 |
| `internal/render/statusbar.go` | Re-introduce connection-state segment (amber border, glyph) — note that LAB-1937 deleted prior `ConnStatus` plumbing, so this is from scratch | 150 |
| **Subtotal (production)** | | **~1630** |
| Tests | Integration coverage using LAB-1953's `newServerHarnessPair` | 800 |
| **Total (production + tests)** | | **~2430** |

Production code is **~1630 LOC**; with the ~800 LOC of tests the total lands at **~2430 LOC**. This is **larger than the original draft's estimate of ~2000 LOC** because that estimate was based on outdated assumptions about surviving plumbing. The skeptic's R2 finding (LAB-1934 cost was lifecycle, not transport) is corrected here: `MirrorManager` is sized realistically at 500 LOC. If `MirrorManager` exceeds 800 LOC during implementation, the worker should comment on the relevant ticket as a blocker.

### Architect-recommended factoring

`MirrorManager` lives in `internal/server/mirror/` (not `internal/server/session_mirror.go`). It is injected into `Session` at construction, following the existing `capture.go` pattern (`internal/server/server.go:500`). The `MirrorManager` owns per-mirror state machines independently of the session event loop, which is the only way the state machines remain unit-testable without spinning up a full server.

## Wire protocol

Already in `main` via PR #826:

```go
// internal/proto/wire.go
MsgTypeListPanes  MsgType = 26 // → server: list leaf panes; server replies via Message.Layout
MsgTypeAttachPane MsgType = 27 // → server: subscribe to one pane (enters restricted mode)
```

v2 needs to discard stale in-flight frames after a reconnect (codex R2). The **epoch is local reader metadata, not remote-stamped wire state** — there is **no epoch field on the wire** and no attach-side handshake. This is the corrected design after a second codex pass surfaced that a remote-stamped epoch is unworkable: `MsgTypeAttachPane` is ID-only (`handleAttachPane` reads only `msg.PaneID`), so the local server has no channel to tell the remote "stamp from epoch N+1," and `MsgTypePaneOutput`'s compact binary frame `[0x01][paneID:4][len:4][data]` (`internal/proto/wire.go:227`) has no room for one anyway.

It does not need one. Each reconnect spawns a **new** SSH child and a **new** socket, so frames are already physically segregated by the connection that delivered them. The `MirrorManager` keeps a per-mirror **connection generation** counter (purely local state):

- On each successful (re)attach, bump the generation and reset the mirror emulator before applying the fresh `MsgTypePaneHistory` bootstrap.
- Tag every inbound frame with the generation of the Link that delivered it.
- Drop any frame whose generation is below the current one — this is how late bytes from a superseded Link's still-draining read goroutine are discarded.

Because the discard is keyed on a locally-owned generation rather than a value on the wire, the existing compact binary `MsgTypePaneOutput` / `MsgTypePaneHistory` frames are reused **unchanged**, and the renderer hot path keeps its compact framing. The only wire additions in v2 remain the gob messages `MsgTypeListPanes`/`MsgTypeAttachPane` (already in PR #826) plus `MsgTypePaneMetaUpdate` (below); there is **no** new PaneOutput/PaneHistory frame variant. `MsgTypePaneHistory` already exists and serves the bootstrap.

### Restricted mode invariants (already enforced in PR #826)

After a connection sends `MsgTypeAttachPane{PaneID: N}`:

**Client → server (what the restricted connection may send):**

- ✓ `MsgTypeInputPane` if and only if `PaneID == N` (other PaneIDs return a non-fatal error; the connection survives).
- ✗ Any other client→server message closes the connection with an error. Specifically: `MsgTypeAttach`, `MsgTypeCommand`, `MsgTypeResize`, `MsgTypeInput` (without PaneID), `MsgTypeUIEvent`, `MsgTypeCaptureResponse`. There are **no exceptions** — including resize (see [Resize policy](#resize-policy), which uses a separate non-restricted connection precisely so this invariant holds).

**Server → client (what the server may send to a restricted connection):**

- ✓ `MsgTypePaneOutput` only for `PaneID == N`. *(already in PR #826)*
- ✓ `MsgTypeExit` if pane N exits, then the connection closes. *(already in PR #826)*
- ✓ `MsgTypePaneHistory` once, as the attach-time bootstrap for pane N. *(new wire addition — see phase 2)*
- ✓ `MsgTypeClipboard` when pane N emits OSC 52. *(new wire addition — must be added to the scoped subscriber path in `session_events_pane.go`)*
- ✓ `MsgTypeBell` when pane N rings the bell. *(new wire addition)*
- ✓ `MsgTypePaneMetaUpdate` when pane N's meta changes. *(new wire addition — see [capability matrix](#capability-matrix-mirror-panes))*

The client→server direction is the security boundary codex flagged in R5 of the v1 review. It is not optional and not relaxable. The server→client list grows in v2 (the four `new wire addition` rows above are explicit implementation-ticket deliverables, not yet in PR #826), but every addition is a server-originated push to a pane-N subscriber — none of them widen what the *client* is allowed to drive.

## Transport

```
ssh -o BatchMode=yes <user>@<host> -- nc -U /tmp/amux-${UID}/${session}
```

The local `amux` server spawns the SSH child process and treats its stdin/stdout as a wire-protocol stream. No remote daemon to install, no new ports, no firewall changes. SSH provides auth, confidentiality, and integrity; the wire protocol provides nothing beyond what local clients get.

**Why `nc -U` and not `ssh -W`:** amux listens on a **Unix domain socket**, and `ssh -W host:port` only forwards to a TCP `host:port`. Reaching a remote Unix socket means either execing a small relay on the remote (`nc -U <socket>`, shown above) or using OpenSSH stream-local forwarding (`ssh -L <local-socket>:<remote-socket>` / `-o StreamLocalBindUnlink`). v1 uses the `nc -U` exec form because it needs no local socket file and no per-connection `-L` bookkeeping; it does require `nc` (or `socat`) on the remote, which the host config can override via an explicit relay command. Stream-local forwarding is a drop-in alternative for hosts without `nc`, tracked as a follow-up rather than a v1 requirement.

**UID/socket discovery** (codex hand-wave): the config entry includes `socket_path = "/tmp/amux-1000/main"` explicitly. amux does *not* try to guess the remote UID — if the user SSHs as a different user than they are locally, they configure the path. A future `amux remote discover <host>` could SSH and report the available sockets, but it is out of scope for v1.

**Test bypass**: `internal/remote/Dialer` is an interface. Production injects an SSH-based implementation; tests inject a `net.Conn` pair that dials the second harness's Unix socket directly. This closes the architect's "no integration test without a live SSH daemon" gap, and composes with LAB-1953's `newServerHarnessPair`.

## UX

### Configuration

```toml
# ~/.config/amux/config.toml
[remote.hosts.hetzner-1]
ssh = "cweill@100.115.94.1"
session = "main"                          # remote session name
socket_path = "/tmp/amux-1000/main"       # required, no guessing
```

### Commands (all under `amux remote`)

```
amux remote add <name> --ssh <ssh-target> --socket <path> [--session <name>]
amux remote list                       # show known hosts + link health
amux remote rm <name>
amux remote panes <name>               # table identical to `amux list`, scoped to remote
amux remote status                     # link health + active mirrors per host
amux remote attach <name>:<pane-name>  # mirror a remote pane (non-interactive, scripting-friendly)
amux remote detach <local-pane>        # drop a mirror (remote pane keeps running)
amux remote resize <local-pane>        # resize the remote pane via a separate connection (affects all its clients)
```

### Spawn with attach (scripting path)

```
amux spawn --at pane-70 --horizontal --attach hetzner-1:pane-1786
```

This is the canonical non-interactive entry point. Equivalent to: spawn a new pane next to pane-70, but instead of starting a local PTY, attach it to the remote pane. **Pane names, not IDs.** Names survive remote server restarts; IDs do not.

### Interactive picker

```
amux remote attach hetzner-1
```

With no pane spec, opens a chooser:

```
┌─ Remote panes: hetzner-1 ─────────────────────┐
│  PANE  NAME         WINDOW    CWD          ▶  │
│  1561  pane-1561    alphaevol ~/research      │
│ *1786  pane-1786    amux      ~/amux       ◀  │
│  1827  pane-1827    alphazero ~/alphago       │
└──────────────────────────────────────────────┘
```

Reuses `internal/client/chooser.go` with a new `Source` interface. The local server queries the remote via `MsgTypeListPanes` and the response renders identically to the local picker.

### Status line

```
[ pane-91 @hetzner-1:pane-1786 ●  main #817  ~/amux ]
```

- `@hetzner-1` uses the existing `icons.RemoteHost` glyph (already rendered for any pane with `PaneMeta.Host != ""`).
- `●` green when connected.
- `⚠ reconnecting… 3s` amber during backoff.
- `✗ remote pane gone` red if the remote pane was killed externally.

**Drop order when the cell is narrow:** cwd → branch → pr → remote pane name → keep `@host ●` as the smallest viable identity. Specified explicitly so reviewers can verify against 40-column splits.

### Reconnect visualization

The UX reviewer flagged that the v1 "dim the content" signal conflicts with the existing inactive-pane dim. v2 corrects this:

- **Border tint amber** during reconnecting state (Catppuccin `peach` from `config/config.go`). Border tinting already exists per-pane in the renderer.
- **Content stays at normal brightness** — readable, not dim.
- **Status line glyph + countdown** is the primary signal.

## State machine

```
   connecting
       │ attach ok
       ▼
   connected ───────────── user closes ─────────────▶ detached
       │  ▲                                            (mirror removed
       │  │ reconnect ok                                from layout)
 ssh   │  │ (gen+1, reset emu,
 drop  │  │  fresh MsgTypePaneHistory)
       ▼  │
   reconnecting ──── retry budget exhausted (5 attempts) ────▶ dead
                                                               (banner stays,
                                                                mirror kept in
                                                                layout; user must
                                                                amux remote attach
                                                                again)
```

`reconnecting` has exactly two exits: **reconnect ok** loops back to `connected` (bumping the local connection generation, resetting the emulator, and replaying a fresh `MsgTypePaneHistory`), and **retry budget exhausted** terminates in `dead`. There is no path from `reconnecting` straight back to `connected` without a successful re-dial.

**Retry budget**: 5 attempts with `1s → 2s → 4s → 8s → 16s` backoff (cap 30s). After exhaustion, mirror transitions to `dead`, stays in layout with a red banner. User decides whether to re-attach.

## Resize policy

**v1 is letterbox-only.**

- The mirror's emulator runs at the remote pane's actual `(cols, rows)`.
- When the local cell is larger than that geometry: pad with blank cells.
- When the local cell is smaller: crop with a visible truncation indicator on the right/bottom edges.

`amux remote resize <local-pane>` does **not** go through the mirror's restricted connection. The scoped subscriber rejects every client→server message except `MsgTypeInputPane` (see [Restricted mode invariants](#restricted-mode-invariants-already-enforced-in-pr-826)), and that boundary stays absolute. Instead, the command opens a **separate, short-lived, non-restricted client connection** to the remote server over the same SSH tunnel, issues a normal pane-resize command for the target pane, and closes. The mirror's scoped connection is untouched; on the next `MsgTypePaneOutput` frame it simply observes the remote pane at its new geometry and re-letterboxes.

This costs one extra dial per explicit resize (rare, user-initiated) but keeps the restricted mode a true invariant rather than an invariant-with-an-asterisk. Because the resize lands on the remote pane itself, it affects every client attached to that pane on the remote — the documented multi-user trade-off.

This is explicit, easy to explain, and avoids the multi-user resize tug-of-war for the common (letterbox) path. A future `--negotiate` mode is additive and out of scope for v1.

## Capability matrix (mirror panes)

For each existing per-pane signal, behavior on a mirror:

| Operation | Mirror pane behavior |
| --- | --- |
| Keyboard input (incl. Ctrl-C) | Forwarded as `MsgTypeInputPane` |
| `amux kill pane-91` | **Local-only**: detach the mirror; the remote pane keeps running. (Sharp edge from UX review — default must be non-destructive.) |
| `amux kill --remote pane-91` | Forwards `kill` to the remote pane via a new command path |
| Resize | Letterbox locally; `amux remote resize` opts into remote resize |
| Zoom | Resizes the local cell only; mirror still letterboxes the remote geometry. Visible content unaltered. |
| Copy mode | Operates on the mirror's local emulator. Selections work; OSC52 clipboard does not propagate from the remote (see below). |
| OSC52 clipboard from remote | **Forwarded.** The remote server emits `MsgTypeClipboard`; the local server applies it to the user's clipboard via the local client (existing flow). |
| Bell | **Forwarded.** Local server raises bell on the mirror's cell. |
| Mouse events | **Forwarded** as `MsgTypeInputPane` with mouse bytes. |
| Bracketed paste | **Forwarded.** Paste markers travel as input bytes. |
| Kitty keyboard protocol | **Forwarded as opaque bytes.** Remote pane decides. |
| Local echo prediction | **Disabled for mirror panes.** RTT over SSH/Tailscale is too variable to make prediction safe; mispredictions corrupt the emulator state. |
| Typing into a reconnecting mirror | **Drop with bell + status-line "input dropped".** Never buffer. Buffering an agent's `/exit` and replaying it post-reconnect would be catastrophic. |
| `amux capture --format json` (local server) | Returns the mirror's local emulator snapshot. |
| `AgentStatus` in capture JSON | **Forwarded *and* given precedence** (see below). Agent status is not stored in `PaneMeta` — it is computed at capture time from the pane's local process tree (`capture_forward.go`, `server_capture_full.go`). A proxy mirror has no local process, so the default path reports it idle. The mirror must store the remote-forwarded status and the capture path must prefer that stored value for proxy panes. Forwarding alone is insufficient. |
| `amux respawn pane-91` | **Forbidden** — error. (Already enforced for proxy panes at `commands_layout.go:581`.) |
| `amux rename pane-91 <name>` | Renames the **local** mirror pane only. The remote pane's name is unchanged. The mirror's remote handle (stored in `RemoteRef.PaneName`) is unaffected. |
| `amux focus pane-91` | Works normally — focus is a local layout operation. The remote pane's active state is unchanged. |
| Local server hot-reload | Mirror enters `reconnecting` after restore; emulator content shown frozen until reconnect succeeds. Checkpoint stores `RemoteRef{host, session, pane_name}`. (Construction ordering caveat — see [Checkpoint and hot-reload](#checkpoint-and-hot-reload).) |
| Remote pane killed externally | Local mirror receives `MsgTypeExit`, status flips to red `✗ remote pane gone`. Stays in layout. |
| Remote server restart | Mirror enters `dead` after the retry budget exhausts. The SSH connection drops when the remote server exits, and within the budget window the local `MirrorManager` cannot distinguish a server that is restarting from one that is gone for good — so v1 deliberately does **not** auto-reconnect across a remote restart, even though the pane *name* would still resolve once the remote is back. Re-attach is a manual `amux remote attach` (the chooser re-resolves the name). See the Decision Log for why this is a deliberate v1 simplification rather than an ID-stability limitation. |

The forwarding mechanism for `AgentStatus` and per-pane meta (`PaneMeta.GitBranch`, `PaneMeta.PR`, `PaneMeta.TrackedPRs`, `PaneMeta.TrackedIssues`) is a new wire message piggybacking on the same restricted mode:

**`PaneMeta.Host` is deliberately excluded from forwarding.** The remote's local panes carry `Host = "local"` (`mux.DefaultHost`), and the status line renders `@<Host>` for the mirror identity. The mirror's `Host` is owned by the *local* server and set to the remote host name (`hetzner-1`) so the status line shows `@hetzner-1` (see [Status line](#status-line)). Forwarding the remote's `Host` would overwrite that with `local` and erase the mirror identity. The same caution applies to any other field whose meaning is host-relative; only branch/PR/issue/agent-status — which describe the *work* in the pane, not its location — are forwarded.

```go
MsgTypePaneMetaUpdate MsgType = 28 // server → restricted client: meta changed for subscribed pane
```

Two-part requirement for agent status specifically (codex P2):

1. **Store** the forwarded status on the mirror pane. Unlike git branch / PR (which already live in `PaneMeta` and serialize today), agent status is computed on demand during capture from the local process tree, so there is no existing field to populate — the mirror needs a dedicated slot to hold the last `MsgTypePaneMetaUpdate` agent status.
2. **Prefer** that stored value in the capture path. `captureAgentStatus` (and the JSON assembly in `capture_forward.go` / `server_capture_full.go`) must, for proxy panes, return the stored remote status instead of inspecting the (nonexistent) local process tree. Without this precedence override, `amux capture --format json` reports every mirror idle and agents misroute work.

Like all inbound frames, a `MsgTypePaneMetaUpdate` is tagged on receipt with the delivering Link's connection generation and dropped if that generation is stale (same local discard rule as `MsgTypePaneOutput`).

## Checkpoint and hot-reload

The architect's critical finding: today's proxy-pane checkpoint restore path (the `NewProxyPaneWithScrollback` call begins at `internal/server/checkpoint.go:260`; the write-discarding lambda `func(data []byte) (int, error) { return len(data), nil }` is at `:262`) installs a write-discarding stub. For mirrors, that path must be replaced with `MirrorManager` rehydration.

**Construction-ordering caveat.** Pane restore happens during checkpoint load, but `MirrorManager` is injected into `Session` at construction. The restore path therefore runs at a point where the `MirrorManager` must already exist to be notified. The implementation must construct `MirrorManager` *before* the pane-restore loop in the checkpoint constructor (`NewServerFromCheckpoint…`), then have restore call into it for each `RemoteRef != nil` pane. If a restored mirror's pane is reconstructed before the manager is wired, the notification in step 2 below is dropped and the mirror stays frozen. This ordering is the v2 analogue of the existing capture-forwarder injection at `server.go:500` and must be covered by the checkpoint round-trip test.

**Checkpoint format addition** (new field, additive — old checkpoints continue to restore as frozen proxies):

```go
type PaneCheckpoint struct {
    // ... existing fields ...
    RemoteRef *struct {
        Host     string   // remote.hosts key
        Session  string   // remote session name
        PaneName string   // durable handle; re-resolved to an ID on each attach
    }
}
```

No epoch is persisted: the connection generation is in-memory local state that resets to its initial value on server reload, and restore re-attaches the mirror fresh.

On restore:

1. If `RemoteRef == nil` → existing proxy stub behavior (frozen). This covers checkpoints from before v2.
2. If `RemoteRef != nil` → `MirrorManager` is notified. It re-dials, runs `MsgTypeListPanes` to resolve `RemoteRef.PaneName` to the remote's current pane ID, re-attaches via `MsgTypeAttachPane{PaneID}` (the wire stays ID-only), bumps the local connection generation, resets the emulator, and replays the fresh `MsgTypePaneHistory` bootstrap.

If reattachment fails (host gone, pane gone, ssh auth changed), the mirror enters `dead`. Banner explains. User decides.

## Tests

The two-server harness from PR #825 (`newServerHarnessPair`) is the foundation. Federation tests:

1. **Happy path**: spawn pane on `remote`, attach from `local`, assert PaneOutput flows, assert input arrives at the remote PTY.
2. **Restricted mode airtightness** (already covered in PR #826's `attach_pane_protocol_test.go`): cannot send `MsgTypeCommand`, cannot send `MsgTypeInputPane` for other panes.
3. **Reconnect generation ordering**: a late `MsgTypePaneOutput` from a superseded Link's still-draining read goroutine is discarded by the local connection-generation check, while frames from the current generation are applied — proving stale-frame protection works with no wire change.
4. **Letterbox**: local cell larger/smaller than remote geometry produces expected padding/cropping in golden files.
5. **Checkpoint round-trip**: local server reload preserves mirror; `MirrorManager` rehydrates and reaches `connected`.
6. **Remote pane killed externally**: local mirror reaches `dead` cleanly.
7. **Remote server restart**: local mirror reaches `dead`, retry budget exhausted, banner visible.
8. **Bell, OSC52, mouse forwarding**: each verified in turn.
9. **Typing during reconnect**: keys dropped, bell raised, status banner shows "input dropped".
10. **`AgentStatus` precedence**: `amux capture --format json` on the local mirror returns the remote's *forwarded* agent status (a busy remote agent shows busy), proving the proxy-pane precedence override beats the default "no local process → idle" computation.
11. **Attach-by-name resolve**: a mirror configured by name attaches to the correct pane after the remote re-IDs that pane (e.g. across a `MsgTypeListPanes` → resolve → `MsgTypeAttachPane{PaneID}` cycle), confirming the name→ID resolve step.

Per CLAUDE.md, each new test gets `-count=100` before merge.

## Phasing

This is roughly 4 PRs against `main`:

1. **Config + `internal/remote/Link` + `Dialer` interface.** No behavioral change yet. Unit tests for SSH child lifecycle (using a fake `ssh` binary).
2. **`MirrorManager` + checkpoint `RemoteRef`.** Wire up the state machine. Integration tests using `newServerHarnessPair` and a `Dialer` stub.
3. **`amux remote` CLI surface + `spawn --attach`.** Glue to user-visible commands. The chooser comes later.
4. **Chooser + per-pane meta forwarding (`MsgTypePaneMetaUpdate`).** Polish.

Each PR independently shippable. After phase 2, mirrors are usable from the command line; phases 3 and 4 are UX polish.

## Out of scope (explicit, deferred)

- **Multi-hop federation** (A → B → C). Design doesn't prevent it; implementation assumes single hop.
- **Cross-version compatibility.** Both sides must run the same `amux` build. Wire protocol capability handshake fails otherwise.
- **A discovery service.** Federation is point-to-point.
- **Per-mirror local echo prediction.** RTT too variable.
- **`--negotiate` resize mode.** Additive to v1.
- **Input arbitration across multiple mirrors of the same remote pane.** v1 does **not** arbitrate: if two mirrors target the same remote pane, input from either is forwarded (both are ordinary restricted subscribers, and the remote pane accepts `MsgTypeInputPane` from each). This matches the open-question below; arbitration (first-writer-wins, lock, etc.) is explicitly deferred.
- **Rate / flood limiting on the remote.** A misbehaving local mirror can flood the remote with many small `MsgTypeInputPane` frames. The existing `maxMessageSize = 16 MB` cap (`wire.go`, enforced per message at decode time) bounds each *frame's size* but is **not** a rate or flood control — a fast stream of small frames is unaffected by it. No per-connection rate limiting ships in v1; it is acknowledged as a gap, not mitigated.
- **`amux events` `--host` filter.** Removed in LAB-1937. Re-introduction tracked separately if needed.

## Decision log

| Date | Decision | Why |
| --- | --- | --- |
| 2026-05-27 | Federate amux-to-amux instead of wrapping SSH-to-shell | Avoids the LAB-1934 model where the remote `amux` wasn't involved. Lets capture/copy-mode/status work via the same code path as local panes. |
| 2026-05-27 | SSH-only transport, no Mosh or QUIC | LAB-1934's Mosh was a stub. QUIC is additive. |
| 2026-05-27 | Restricted-mode `MsgTypeAttachPane` (security boundary) | Codex review R5. Without it, a federated client can run any server command. Verified airtight in PR #826. |
| 2026-05-27 | `amux remote <subcommand>` namespace | UX review #6. Avoids verb sprawl. |
| 2026-05-27 | `spawn --at ... --attach host:pane-name` (not `--mirror`) | UX review #1, #2. `split-right` doesn't exist; `mirror` implies read-only. |
| 2026-05-27 | Pane *names* are the durable handle (persisted in `RemoteRef`), resolved to IDs at attach time | "Names over IDs" principle. IDs are session-monotonic and not stable across remote restart. (Superseded the original "names on the wire" wording — the wire attach is ID-only; see the 2026-05-28 entry.) |
| 2026-05-27 | Letterbox at remote geometry; `amux remote resize` for explicit opt-in | Avoids multi-user resize tug-of-war. v1 keeps the matrix of behaviors small. |
| 2026-05-27 | `MirrorManager` in `internal/server/mirror/`, injected into `Session` | Architect #3. Matches the existing `capture.go` injection pattern. |
| 2026-05-27 | `Dialer` interface for test bypass | Architect #5. Integration tests use direct Unix socket dial, not a real SSH daemon. |
| 2026-05-27 | Drop keys + bell when typing into a reconnecting mirror; never buffer | UX sharp edge. Buffering an agent's `/exit` for replay is catastrophic. |
| 2026-05-27 | `amux kill` is local-only on mirrors; `--remote` flag for upstream | UX sharp edge + CLAUDE.md "destructive pane actions need confirmation". |
| 2026-05-28 | Tracer bullet (PR #826) proves scoped subscription doesn't tangle with broadcast | +70 LOC in `client_conn.go`, well under 300 threshold. Cleared the go/no-go premise. |
| 2026-05-28 | Stale-frame protection uses a **local connection-generation counter**, not a wire epoch | Codex review R2, then the second pane-91 codex pass. Each reconnect is a new socket, so frames self-segregate by Link; the local server tags and drops by generation. No wire change, no attach handshake. Supersedes the earlier remote-stamped-epoch / binary-frame-variant idea. |
| 2026-05-28 | `MsgTypePaneMetaUpdate = 28` for forwarding per-pane meta (AgentStatus, GitBranch, PR) | Architect #4 + codex hand-waved item. Without this, agents misroute mirrors as idle. |
| 2026-05-28 | LOC budget ~1630 production + ~800 tests = ~2430 total, realistic vs the original draft's ~2000 | Skeptic R2. Original budget mis-attributed deleted code. |
| 2026-05-28 | Checkpoint stores `RemoteRef{host, session, pane_name}` (no epoch — generation is in-memory); old checkpoints restore as frozen (back-compat) | Architect critical fix. |
| 2026-05-28 | `amux remote resize` uses a separate non-restricted connection, **not** the mirror's scoped tunnel | Review of PR #827 (Claude + Greptile P1). Keeps the restricted-mode security boundary an absolute invariant instead of an invariant-with-an-exception. Resolves the contradiction between the invariants list and the resize policy. |
| 2026-05-28 | v1 does **not** auto-reconnect a mirror across a remote *server* restart, even when the pane name would still resolve | Review of PR #827 (Claude non-blocking #3). The SSH drop on remote exit is indistinguishable from "remote gone" within the retry-budget window, so the mirror terminates in `dead` and the user re-attaches manually. This is a deliberate v1 simplification, not an ID-stability limitation. |
| 2026-05-28 | Restricted-mode server→client send list is explicit and grows in v2 (adds PaneHistory, Clipboard, Bell, PaneMetaUpdate) | Review of PR #827 (Claude non-blocking #2). PR #826 sends only PaneOutput + Exit; the four additions are tracked implementation-ticket deliverables so they aren't silently missed. |
| 2026-05-28 | `MirrorManager` must be constructed before the checkpoint pane-restore loop | Review of PR #827 (Claude open-question #5). Otherwise restore-time mirror notifications are dropped and mirrors stay frozen after hot-reload. |
| 2026-05-28 | Wire `MsgTypeAttachPane` stays **ID-only**; names are resolved via `MsgTypeListPanes` before each attach | pane-91 codex review (P1). PR #826's `handleAttachPane` reads `msg.PaneID`; the spec's `{Name: ...}` form and "names on the wire" wording contradicted that. `RemoteRef.PaneName` is the durable handle; the resolve step bridges it to the current ID. |
| 2026-05-28 | ~~Subscription epoch is a wire-format addition (binary frame variants `0x03`/`0x04`)~~ **SUPERSEDED same day** by the local-generation decision above | First pane-91 codex pass (P1) established the gob field is dropped by the compact binary frames; the second pass showed a remote-stamped epoch needs an attach handshake that doesn't exist — so the epoch became local-only and no wire-format addition is needed. Kept for traceability. |
| 2026-05-28 | Mirror agent status must be **stored on the mirror and preferred in the capture path**, not just forwarded | pane-91 codex review (P2). `captureAgentStatus` computes status from the local process tree; a proxy mirror has none, so forwarding alone still reports idle. The capture path needs a proxy-pane precedence override. |

## Open questions

1. **Where do per-host `socket_path` strings live when the user has 20 hosts?** Inline in `config.toml` is fine for 1–5 hosts. Beyond that, a `[remote.hosts.*.socket_path]` discovery mode (`amux remote discover <host>`) is a natural follow-up.
2. **What is the right behavior when two clients on hetzner-xl both mirror the same remote pane?** Both render correctly (output fanout works fine on the remote). Input from either is forwarded. There's no arbitration. Worth observing in practice before adding constraints.
3. **Should `MirrorManager` also handle the inverse case — exposing a local pane as mirrorable by a remote?** Strictly symmetric; falls out of `MsgTypeAttachPane` being already implemented. v1 doesn't ship this, but the wire format doesn't preclude it.

## Implementation tickets

To be filed after this spec is approved:

- LAB-XXXX: `internal/remote/Link` + `Dialer` interface + config restoration.
- LAB-XXXX: name→ID resolve helper on `MsgTypeListPanes` (small wire-foundation; the `MirrorManager` ticket depends on it). No PaneOutput/PaneHistory frame change — stale-frame protection is local connection-generation state inside `MirrorManager`.
- LAB-XXXX: `internal/server/mirror/MirrorManager` + state machine + connection-generation discard + checkpoint `RemoteRef` (constructed before the restore loop).
- LAB-XXXX: `amux remote add/list/rm/panes/attach/detach/resize` CLI surface (resize via separate non-restricted connection).
- LAB-XXXX: `spawn --attach host:pane-name` glue.
- LAB-XXXX: `internal/client/chooser.go` Source interface + remote source.
- LAB-XXXX: `MsgTypePaneMetaUpdate` + AgentStatus **forwarding and capture-path precedence override** for proxy panes.
- LAB-XXXX: Status line connection-state segment + amber border tint (re-introduce post-LAB-1937).

Each ticket should reference this spec and call out any deviations as blocker comments.
