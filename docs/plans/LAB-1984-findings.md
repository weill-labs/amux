# LAB-1984 — Research findings: window-level remote mirroring

## Goal
Add the ability to mirror an entire remote *window* (all its panes, in their
split layout) into a new local window, parallel to today's single-pane
`amux remote attach <host>:<pane>`.

## How single-pane mirroring works today (verified file:line)

- **CLI dispatch** — `internal/server/commands_remote.go:73-101` (`cmdRemote` →
  `runRemoteCommand` switch). Subcommands: add/list/rm/panes/status/attach/detach/resize.
- **`remote attach <host>:<pane>`** — `commands_remote.go:249-296`. Splits on `:`,
  builds `checkpoint.RemoteRef{Host,Session,PaneName}`, then in a mutation:
  1. `prepareMirrorPane(meta, ref, w.Width, contentHeight)` — constructs a proxy
     pane, registers it in `s.Panes`, does **not** insert it into a window
     (`session_pane.go:173-192`).
  2. `w.SplitPaneWithOptions(...)` — inserts the leaf into the active window.
  3. `trackMirrorPane(pane, ref)` → `mirror.Manager.Track` — starts the mirror.
- **Mirror lifecycle** — `internal/server/mirror/manager.go`. `mirrors map[uint32]*mirrorState`
  keyed by **local pane ID**. Per mirror: one SSH `Link`, resolves remote pane ID
  by name, sends `MsgTypeAttachPane`, then `readLoop` feeds `MsgTypePaneOutput`
  bytes into the local emulator via `pane.FeedOutput`. Input goes back out via
  `Manager.Write` → `MsgTypeInputPane` (manager.go:202-228).
  Handled inbound msgs: PaneHistory, PaneOutput, PaneMetaUpdate, Exit, CmdResult —
  **no layout updates** (manager.go:537-563).
- **Transport** — `internal/remote/link.go` (SSHDialer spawns `ssh <target> -- nc -U <socket>`),
  `internal/remote/resolve.go` (`ListPanes` → `MsgTypeListPanes`/`MsgTypeLayout`,
  `ResolvePaneID`). `remote.ListPanes` already returns the FULL multi-window layout
  (`proto.LayoutSnapshot.Windows []WindowSnapshot`).
- **Persistence** — `checkpoint.RemoteRef{Host,Session,PaneName}` per proxy pane.
  Local layout (windows + splits) is checkpointed separately. On reload each proxy
  pane re-tracks independently (`internal/server/checkpoint.go`).

## Linchpin for reuse
- **`mux.RebuildWindowFromSnapshot(ws proto.WindowSnapshot, width, height int, paneMap map[uint32]*Pane) *Window`**
  (`internal/mux/snapshot.go:164`) builds a local `Window` matching a remote
  `WindowSnapshot`'s split tree, wiring in proxy panes keyed by remote pane ID.
  This means window mirroring is mostly: make N proxy panes → build paneMap →
  RebuildWindowFromSnapshot → append window → Track each pane.
- **`runNewWindow`** (`commands_layout.go:884-919`) is the canonical "append a new
  local window + activate" path: `nextWindowID()`, set ID/Name, append to
  `sess.Windows`, `activateWindow`.

## Key consequence: MVP can reuse per-pane machinery wholesale
Because each remote window pane becomes an ordinary independent proxy pane:
- Input routing already works (active local pane's write override → its remote ID).
- `remote detach <pane>` / `remote resize <pane>` already work per pane.
- **Checkpoint/restore survives reload for free**: each pane re-tracks via its
  own RemoteRef, and the local window shape is restored from the local layout
  checkpoint. No new checkpoint code needed for the MVP.

## Limitations to defer (Phase 2+)
1. **Static snapshot** — the per-pane subscription receives no layout broadcasts,
   so remote split add/remove after attach is NOT reflected. Re-attach to refresh.
2. **Dimension matching** — local window may differ from the remote's geometry;
   per-pane `remote resize` still applies. Window-level resize is future work.
3. **Whole-window detach** — MVP relies on closing the local window or detaching
   panes individually; a single `remote detach --window` is a nice-to-have.
